package main

import (
	"context"
	"log/slog"
	"os"
)

// main is intentionally tiny: all startup work lives in run so the executable
// boundary only handles process concerns like logging the final error and exit.
//
// The one argument it accepts is "readyz", which probes THIS process's own
// readiness endpoint and exits 0 or 1. It exists because the release image is
// distroless: there is no curl, wget or shell in it, so a container healthcheck
// has nothing else to call. Without it the orchestrator's only options are no
// healthcheck at all or a shell in the production image.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "readyz" {
		if err := selfReadyz(context.Background()); err != nil {
			slog.Error("readiness probe failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("Server startup failed", "error", err)
		os.Exit(1)
	}
}
