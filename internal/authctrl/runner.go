package authctrl

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/maintainerd/core/internal/steward"
)

// Runner converges the control catalog through the post-setup control path.
//
// It runs on EVERY boot, not only after a fresh setup. That is what makes the
// loop convergent: whatever a previous run finished, crashed through, or never
// reached, a boot re-derives. Because every write is get-or-create, a converged
// install performs zero writes and simply reports "ensured".
type Runner struct {
	client   *Client
	catalog  steward.Catalog
	keys     KeyStore
	registry Registry

	// Single-flight: the boot loop and an operator's POST must not converge
	// concurrently. Two passes interleaving would each see the other's
	// half-written state and could double-create the objects auth's regular
	// surface does NOT make idempotent.
	mu      sync.Mutex
	running bool
	last    *Report
}

// ErrApplyRunning is returned when a reconcile pass is already in flight.
var ErrApplyRunning = errors.New("authctrl: a steward apply is already running")

func NewRunner(client *Client, catalog steward.Catalog, keys KeyStore, registry Registry) *Runner {
	return &Runner{client: client, catalog: catalog, keys: keys, registry: registry}
}

// Last returns the most recent pass's report, or nil if none has run.
func (r *Runner) Last() *Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func (r *Runner) tryBegin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

func (r *Runner) end(rep *Report) {
	r.mu.Lock()
	r.running = false
	if rep != nil {
		r.last = rep
	}
	r.mu.Unlock()
}

// Run performs one reconcile pass.
//
// The returned error covers only failures that prevented the pass from running
// at all (no control identity, no connection). Per-object failures live in the
// Report: they are expected, recoverable, and must not look like a dead control
// path.
func (r *Runner) Run(ctx context.Context) (*Report, error) {
	if !r.tryBegin() {
		return nil, ErrApplyRunning
	}
	var report *Report
	defer func() { r.end(report) }()

	if err := r.client.Connect(ctx); err != nil {
		return nil, err
	}

	started := time.Now()
	applier := NewApplier(r.client, r.keys, r.registry)
	totals, err := steward.Reconcile(ctx, r.catalog, applier, r.keys)

	report = &Report{
		Totals:    totals,
		Outcomes:  applier.Outcomes(),
		Transport: TransportControlClient,
		StartedAt: started,
		Duration:  time.Since(started).String(),
	}
	for _, o := range report.Outcomes {
		switch o.Status {
		case StatusCreated:
			report.Created++
		case StatusEnsured:
			report.Ensured++
		case StatusFailed:
			report.Failed++
		}
	}
	if err != nil {
		// Reconcile only aborts on a malformed catalog or a missing KeySink — a
		// programming error in the catalog, not a transport failure. Record it as
		// a pass-level outcome so it is visible in the report too.
		report.Outcomes = append(report.Outcomes, Outcome{Kind: "Catalog", Name: "reconcile", Status: StatusFailed, Error: err.Error()})
		report.Failed++
		return report, err
	}
	return report, nil
}

// Backoff bounds for the boot loop.
const (
	bootBaseBackoff = 5 * time.Second
	bootMaxBackoff  = 5 * time.Minute
	bootMaxAttempts = 12
)

// RunWithRetry converges in the background at boot, retrying with exponential
// backoff. It NEVER panics or exits the process: auth being unreachable, or
// setup not having run yet, is an ordinary startup condition, not a reason to
// take core down. After the attempt cap it gives up and leaves the work to
// POST /api/v1/steward/apply, so a permanently broken control path does not
// retry forever in the background.
func (r *Runner) RunWithRetry(ctx context.Context) {
	backoff := bootBaseBackoff
	for attempt := 1; attempt <= bootMaxAttempts; attempt++ {
		report, err := r.Run(ctx)
		switch {
		case err == nil && report != nil && report.Failed == 0:
			slog.Info("steward: control catalog converged",
				"created", report.Created, "ensured", report.Ensured, "transport", report.Transport)
			return
		case errors.Is(err, ErrNoControlIdentity):
			// Setup has not issued core its control credential yet. Expected on a
			// fresh install; the setup orchestrator is racing us to create it.
			slog.Info("steward: waiting for setup to provision the control identity",
				"attempt", attempt, "retry_in", backoff.String())
		case errors.Is(err, ErrApplyRunning):
			slog.Info("steward: another apply is in flight — will re-check", "retry_in", backoff.String())
		case err != nil:
			slog.Warn("steward: control catalog apply failed — will retry",
				"attempt", attempt, "err", err, "retry_in", backoff.String())
		default:
			slog.Warn("steward: control catalog partially applied — will retry",
				"attempt", attempt, "failed", report.Failed, "retry_in", backoff.String())
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < bootMaxBackoff {
			backoff *= 2
			if backoff > bootMaxBackoff {
				backoff = bootMaxBackoff
			}
		}
	}
	slog.Warn("steward: giving up on background convergence after the attempt cap; "+
		"re-run it with POST /api/v1/steward/apply once auth is reachable",
		"attempts", bootMaxAttempts)
}
