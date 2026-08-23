package main

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maintainerd/core/internal/dependency"
	"github.com/maintainerd/core/internal/platform/authz"
	"github.com/maintainerd/core/internal/platform/config"
	"github.com/maintainerd/core/internal/secretctrl"
	appserver "github.com/maintainerd/core/internal/server"
	"github.com/maintainerd/core/internal/setup"
)

// dependencyProbes are the reachability checks the convergence loop runs.
//
// Nothing here gates boot. auth, core and secret start together and core
// configures them in turn, so "not listening yet" is the normal early state —
// see internal/dependency for why this converges rather than refuses. What DOES
// refuse at boot is incomplete configuration (internal/platform/config).
//
// In development an unconfigured dependency is not probed at all: there is
// nothing to reach, and a permanently-unhealthy check would make /readyz useless
// on a laptop. Outside development config.Init has already refused that state, so
// both probes are always present where they matter.
func dependencyProbes(secret *secretctrl.Client) []dependency.Probe {
	var probes []dependency.Probe
	if config.AuthJWKSURL != "" {
		probes = append(probes, dependency.Probe{Name: "auth", Check: probeAuth})
	}
	if secret.Configured() {
		probes = append(probes, dependency.Probe{Name: "secret", Check: secret.Probe})
	}
	return probes
}

// probeAuth fetches auth's JWKS.
//
// THE CHEAPEST HONEST PROBE. auth serves /healthz and /readyz too, but core
// already knows the JWKS URL and needs no extra variable to reach it, and the key
// set is precisely what core depends on auth for: an auth that answers /healthz
// while its JWKS 404s is not usable by core, and this probe says so. Fetching it
// is also what the token verifier does, so a passing probe means verification
// will work.
func probeAuth(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, config.DependencyProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.AuthJWKSURL, nil)
	if err != nil {
		return fmt.Errorf("build jwks request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Drained so the connection is reusable. The body is a public key set, but it
	// is deliberately not parsed here — that is the verifier's job, and a probe
	// that reimplements it would be a second thing to keep in step.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks endpoint answered %d", resp.StatusCode)
	}
	return nil
}

// readinessChecks are the dependencies /readyz reports on.
//
// THREE CHECKS, ALL FAIL-CLOSED:
//
//	database  a live Ping. Core's own store, in-process and cheap. A replica that
//	          cannot reach it can only produce errors, so it should not be
//	          receiving traffic. Note this is READINESS, not liveness — a database
//	          blip takes replicas out of rotation, it does not restart them.
//	auth      the convergence loop's LAST OBSERVED state, plus the guard's
//	          posture. ModeUnavailable means auth is required here and not
//	          configured, which is exactly the state in which this replica must
//	          not answer: it cannot verify a token, so it cannot authorize.
//	          ModeDevOpen reports ready — a development instance is deliberately
//	          open and has already said so at boot in the loudest terms the log
//	          allows.
//	secret    the convergence loop's last observed state.
//
// The two dependency checks read CACHED state rather than probing live, for two
// reasons. /readyz is unauthenticated, so a live probe would let anyone drive
// core's outbound requests at auth and secret. And the loop is already checking
// on a fixed cadence, so a cached read is at most one interval stale while
// costing nothing — including when an orchestrator probes every second.
func readinessChecks(pool *pgxpool.Pool, rec *dependency.Reconciler, guard authz.Guard, secret *secretctrl.Client, secretSetup *setup.SecretOrchestrator) []appserver.ReadinessCheck {
	return []appserver.ReadinessCheck{
		{
			Name:  "database",
			Probe: func(ctx context.Context) error { return pool.Ping(ctx) },
		},
		{
			// ONE answer about auth, not two: the posture and the reachability are
			// both "can this replica authorize a request".
			Name: "auth",
			Probe: func(context.Context) error {
				switch guard.Mode {
				case authz.ModeUnavailable:
					return fmt.Errorf("authorization is unavailable: %s", guard.Reason)
				case authz.ModeDevOpen:
					return nil
				}
				return rec.State("auth")
			},
		},
		{
			Name: "secret",
			Probe: func(context.Context) error {
				if !secret.Configured() {
					// Development with no vault. Reported ready for ModeDevOpen's
					// reason: the instance is deliberately running without it, and any
					// provision that needs secret material still fails closed.
					return nil
				}
				return rec.State("secret")
			},
		},
		{
			// REACHABLE IS NOT PROVISIONED. The check above answers "can core talk to
			// secret"; this one answers "has secret been set up". They fail
			// independently and an operator needs to tell them apart: a vault that
			// answers but was never provisioned looks healthy on every network probe
			// and refuses every real call. Nothing else surfaces that state, which is
			// why SecretOrchestrator keeps it.
			//
			// No-ops when core is not the controller (SECRET_SETUP_ADDR unset — the
			// standalone case, where an operator drives secret's own REST wizard and
			// core has no opinion about whether that happened).
			Name: "secret-setup",
			Probe: func(context.Context) error {
				if secretSetup == nil || !secretSetup.Enabled() {
					return nil
				}
				return secretSetup.State()
			},
		},
	}
}
