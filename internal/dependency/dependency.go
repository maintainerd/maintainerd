// Package dependency converges core toward "auth and secret are reachable".
//
// WHY THIS IS A LOOP AND NOT A BOOT GATE. auth, core and secret start at the
// same time; core then configures auth and afterwards configures secret. So core
// booting before secret is listening is the NORMAL case, not an error, and a
// process that exited over it would never come up on a cold stack — it would just
// restart until the ordering happened to be lucky. Reachability is therefore a
// state to converge toward, the same level-triggered pattern as the agent's
// convergence loop and core's supervision loop.
//
// WHAT IS STILL A HARD BOOT ERROR: incomplete CONFIGURATION. A missing or
// malformed variable cannot be converged — nothing will arrive to fix it — so
// internal/platform/config refuses it at boot, naming every missing variable at
// once.
//
// WHAT THIS LOOP DOES NOT DO: it never exits the process and it never returns an
// error. Before anything has settled the effect is that /readyz says not-ready,
// so an orchestrator withholds traffic. After a dependency has settled and then
// drops, the effect is exactly the same — not-ready — and core keeps running:
// existing workloads keep running, the console still answers, and the operator
// sees a degraded control plane instead of an outage. A vault blip must not
// become a control-plane outage.
//
// Failures reach the log; the readiness body sees only a dependency name and
// ok/unhealthy (see internal/server).
package dependency

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Probe is one dependency's reachability check.
type Probe struct {
	// Name is the dependency's name — "auth", "secret". It appears in the
	// readiness body, so it is a name and never an address.
	Name string
	// Check reports reachability. It must respect the context and bound itself;
	// the loop shares one deadline across a pass.
	Check func(context.Context) error
}

// Options tunes the cadence. Zero values take the documented defaults.
type Options struct {
	// Interval is the steady-state cadence once every dependency has settled.
	// This is what makes the loop level-triggered rather than a one-shot wait: it
	// keeps checking, so a dependency that dies later is noticed within one
	// interval instead of at the next restart.
	Interval time.Duration

	// InitialBackoff / MaxBackoff bound the retry cadence while something is
	// unsettled. It starts fast because a cold stack usually settles in seconds,
	// and backs off so an outage does not turn into a tight retry loop against a
	// service that is already struggling.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration

	// PassTimeout bounds one pass over every probe.
	PassTimeout time.Duration
}

// Defaults, argued where they are used.
const (
	defaultInterval       = 15 * time.Second
	defaultInitialBackoff = time.Second
	defaultMaxBackoff     = 30 * time.Second
	defaultPassTimeout    = 10 * time.Second
)

// ErrNotProbed is a dependency's state before the first pass completes.
//
// It is an ERROR, not an empty state: "we have not looked yet" and "it answered"
// must not be the same value, or core would report itself ready for the window
// between the listener opening and the first pass landing.
var ErrNotProbed = errors.New("dependency has not been probed yet")

// Reconciler runs the convergence loop and holds the last observed state.
//
// Safe for concurrent use: the loop writes, /readyz reads.
type Reconciler struct {
	probes []Probe
	opts   Options

	mu    sync.RWMutex
	state map[string]error
}

func New(probes []Probe, opts Options) *Reconciler {
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	if opts.InitialBackoff <= 0 {
		opts.InitialBackoff = defaultInitialBackoff
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = defaultMaxBackoff
	}
	if opts.PassTimeout <= 0 {
		opts.PassTimeout = defaultPassTimeout
	}
	state := make(map[string]error, len(probes))
	for _, p := range probes {
		state[p.Name] = ErrNotProbed
	}
	return &Reconciler{probes: probes, opts: opts, state: state}
}

// Names lists the dependencies in declaration order.
func (r *Reconciler) Names() []string {
	out := make([]string, 0, len(r.probes))
	for _, p := range r.probes {
		out = append(out, p.Name)
	}
	return out
}

// State returns the last observed error for one dependency, or nil when it was
// last seen healthy. Unknown names report ErrNotProbed rather than nil, so a
// typo in a readiness wiring cannot read as healthy.
func (r *Reconciler) State(name string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	err, ok := r.state[name]
	if !ok {
		return ErrNotProbed
	}
	return err
}

// Settled reports whether every dependency was healthy on the last pass.
func (r *Reconciler) Settled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, err := range r.state {
		if err != nil {
			return false
		}
	}
	return true
}

// Run converges until ctx is cancelled. It never returns an error and never
// exits the process — see the package doc.
func (r *Reconciler) Run(ctx context.Context) {
	if len(r.probes) == 0 {
		return
	}
	delay := r.opts.InitialBackoff
	for {
		r.pass(ctx)
		if ctx.Err() != nil {
			return
		}
		if r.Settled() {
			delay = r.opts.Interval
		} else if delay < r.opts.MaxBackoff {
			delay *= 2
			if delay > r.opts.MaxBackoff {
				delay = r.opts.MaxBackoff
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// Pass runs one convergence pass. Exported for tests and for a caller that wants
// a first reading before it starts serving.
func (r *Reconciler) Pass(ctx context.Context) { r.pass(ctx) }

// pass probes every dependency CONCURRENTLY under one deadline, so a pass costs
// the slowest probe rather than the sum, and one unreachable dependency does not
// delay learning about the others.
func (r *Reconciler) pass(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, r.opts.PassTimeout)
	defer cancel()

	results := make([]error, len(r.probes))
	var wg sync.WaitGroup
	for i, p := range r.probes {
		if p.Check == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = p.Check(ctx)
		}()
	}
	wg.Wait()

	for i, p := range r.probes {
		r.record(p.Name, results[i])
	}
}

// record stores an outcome and logs only TRANSITIONS.
//
// Logging every tick would bury the one line that matters — the moment a
// dependency came back — under a page of identical warnings, and on a stack that
// takes a minute to settle it would be a minute of noise saying nothing new. The
// FIRST observation always logs, whichever way it went: "not probed yet" is not a
// state anything should transition silently out of.
func (r *Reconciler) record(name string, err error) {
	r.mu.Lock()
	prev, existed := r.state[name]
	r.state[name] = err
	r.mu.Unlock()

	first := !existed || errors.Is(prev, ErrNotProbed)
	switch {
	case err == nil && (first || prev != nil):
		slog.Info("dependency reachable", "dependency", name)
	case err != nil && (first || prev == nil):
		// The cause is logged HERE and nowhere else — it may name an address, which
		// is why the readiness body carries only the name.
		slog.Warn("dependency unreachable — core keeps serving and reports not-ready",
			"dependency", name, "err", err)
	}
}
