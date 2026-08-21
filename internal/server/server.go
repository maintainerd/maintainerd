package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/maintainerd/core/internal/app"
	"github.com/maintainerd/core/internal/platform/authz"
)

// Router builds the core HTTP API. Every route under /api/v1 is behind the
// authz guard (bearer token + route→permission map, fail-closed); /healthz
// sits outside the group so liveness probes need no credentials, and the
// setup surface carries its own CORE_SETUP_TOKEN gate (see internal/setup).
func Router(a *app.App, guard authz.Guard) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(guard.Middleware)
		r.Mount("/tenants", a.Tenant.Routes())
		r.Mount("/projects", a.Project.Routes())
		r.Mount("/services", a.Service.Routes())
		r.Mount("/providers", a.Provider.Routes())
		r.Mount("/agents", a.Agent.Routes())
		r.Mount("/resources", a.Resource.Routes())
		r.Mount("/setup", a.Setup.Routes())
	})

	return r
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
