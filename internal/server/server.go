package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/maintainerd/core/internal/app"
	"github.com/maintainerd/core/internal/platform/authz"
)

// ReadinessCheck is one dependency /readyz probes.
type ReadinessCheck struct {
	// Name is what appears in the response body. It is a DEPENDENCY name —
	// "database", "auth", "secret" — and never an address, a driver message or a
	// credential: the probe is unauthenticated, so its body is public. An
	// operator learns which dependency is down; the cause goes to the log.
	Name string
	// Probe must respect the context. /readyz bounds every probe, and a probe
	// that ignores the bound makes the readiness endpoint itself the thing that
	// hangs.
	Probe func(context.Context) error
}

// readinessTimeout bounds the whole /readyz pass. The individual probes carry
// their own DEPENDENCY_PROBE_TIMEOUT; this is the backstop for one that ignores
// its context.
const readinessTimeout = 5 * time.Second

// Router builds the core HTTP API. Every route under /api/v1 is behind the
// authz guard (bearer token + route→permission map, fail-closed); /healthz and
// /readyz sit outside the group so probes need no credentials, the setup
// surface carries its own CORE_SETUP_TOKEN gate (see internal/setup), and
// /api/v1/capabilities is anonymous by necessity (see capabilities.go and
// authz.exemptSegments).
//
// TWO PROBES, ANSWERING DIFFERENT QUESTIONS.
//
// /healthz is LIVENESS: is this process still able to serve? It touches nothing.
// An orchestrator RESTARTS a container whose liveness probe fails, so a liveness
// probe that reached the database would turn a database blip into a rolling
// restart of every replica.
//
// /readyz is READINESS: should this replica receive traffic right now? It probes
// the dependencies a request actually needs and fails closed. This is where a
// dependency that dies AFTER boot lands: core keeps running — existing workloads
// keep running, the console still answers — and reports itself unready instead of
// exiting. A vault blip must not become a control-plane outage.
func Router(a *app.App, guard authz.Guard, checks []ReadinessCheck, caps CapabilityInfo) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	r.Get("/readyz", readyz(checks))

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(guard.Middleware)
		// Mounted INSIDE the guarded group even though the guard exempts it:
		// keeping it here means the exemption is a single declared fact
		// (authz.exemptSegments) rather than a second, silent one created by
		// mounting outside the group. Registered with Get only, so a mutating
		// verb is a 405 from chi even if the guard ever widened.
		mountCapabilities(r, caps, guard)
		r.Mount("/tenants", a.Tenant.Routes())
		r.Mount("/projects", a.Project.Routes())
		r.Mount("/services", a.Service.Routes())
		r.Mount("/providers", a.Provider.Routes())
		r.Mount("/agents", a.Agent.Routes())
		r.Mount("/resources", a.Resource.Routes())
		r.Mount("/events", a.Event.Routes())
		r.Mount("/setup", a.Setup.Routes())
		r.Mount("/steward", a.Steward.Routes())
	})

	return r
}

// readyz runs every check concurrently under one deadline and answers 200 only
// if all of them pass.
//
// Concurrent and sharing one deadline, so the endpoint's worst case is the
// timeout rather than the sum of the probes. A probe that ignores its context is
// reported as failing when the deadline passes rather than waited on: a readiness
// endpoint that hangs is read as healthy by some orchestrators and unhealthy by
// others, which is the one answer worse than either.
func readyz(checks []ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		errs := make([]error, len(checks))
		var wg sync.WaitGroup
		for i, check := range checks {
			if check.Probe == nil {
				continue
			}
			wg.Add(1)
			go func(i int, probe func(context.Context) error) {
				defer wg.Done()
				done := make(chan error, 1)
				go func() { done <- probe(ctx) }()
				select {
				case err := <-done:
					errs[i] = err
				case <-ctx.Done():
					errs[i] = ctx.Err()
				}
			}(i, check.Probe)
		}
		wg.Wait()

		results := make(map[string]string, len(checks))
		ready := true
		for i, check := range checks {
			if errs[i] != nil {
				ready = false
				results[check.Name] = "unhealthy"
				// The CAUSE goes to the log, never to the response: a driver error
				// names hosts, databases and sometimes users.
				slog.Warn("readiness check failed", "check", check.Name, "err", errs[i])
				continue
			}
			results[check.Name] = "ok"
		}

		status := http.StatusOK
		body := map[string]any{"status": "ready", "checks": results}
		if !ready {
			status = http.StatusServiceUnavailable
			body["status"] = "not_ready"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}

// StartRESTServer runs the HTTP server and shuts it down gracefully when ctx is
// cancelled (SIGINT/SIGTERM).
func StartRESTServer(ctx context.Context, handler http.Handler) error {
	addr := portOrDefault(os.Getenv("HTTP_PORT"), ":8080")
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("REST server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down REST server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func portOrDefault(v, def string) string {
	if v == "" {
		return def
	}
	if !strings.HasPrefix(v, ":") {
		return ":" + v
	}
	return v
}
