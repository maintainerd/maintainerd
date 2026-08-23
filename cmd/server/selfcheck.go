package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// selfReadyzTimeout bounds the self-probe. Kept under a typical healthcheck
// interval so a hung probe is reported as a failure by this process rather than
// killed by the orchestrator, which would look identical to a crash.
const selfReadyzTimeout = 4 * time.Second

// selfReadyz asks this container's own /readyz whether it should be receiving
// traffic, and exits accordingly. It reads no configuration beyond the listen
// port, so it works before — and independently of — config.Init.
//
// It targets loopback deliberately: the point is to ask THIS process, not
// whatever a service name currently resolves to.
func selfReadyz(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, selfReadyzTimeout)
	defer cancel()

	url := "http://127.0.0.1" + portOrDefault(os.Getenv("HTTP_PORT"), ":8080") + "/readyz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		// The status is the whole answer; the body names dependencies and belongs
		// to whoever curls the endpoint, not to a healthcheck's exit code.
		return fmt.Errorf("readyz answered %d", resp.StatusCode)
	}
	return nil
}

// portOrDefault normalizes a port into a listen suffix. It mirrors the REST
// server's own resolution (internal/server) so the probe and the listener cannot
// disagree about where this process is listening.
func portOrDefault(v, def string) string {
	if v == "" {
		return def
	}
	if v[0] != ':' {
		return ":" + v
	}
	return v
}
