package secretctrl

import (
	"context"
	"fmt"
	"strings"
)

// Credentials is the provisioning-side view of the vault: the seam
// internal/deploytemplate writes a generated credential through.
//
// It is a distinct type rather than a method set on Client so the address policy
// — which project, which environment, which folder a provisioned credential
// lands in — lives in ONE place. A call site that chose its own address would be
// a credential an operator cannot find.
type Credentials struct {
	client      *Client
	project     string
	environment string
}

// NewCredentials binds the address policy to a client.
//
// A nil client is accepted and yields a sink that FAILS CLOSED on every write
// (ErrNotConfigured). That is the point: development runs with no vault, and the
// provisioning path must take the same refusing branch there as it does against
// a configured vault that is down — never a branch that inlines the value
// instead.
func NewCredentials(c *Client, project, environment string) *Credentials {
	return &Credentials{client: c, project: project, environment: environment}
}

// Store writes a provisioned credential and returns the inert reference the
// workload spec carries in place of the value.
//
// The address is deterministic — folder = the template, key = the parameter — so
// re-provisioning the same template writes a new VERSION of the same secret
// rather than a second copy under a name nobody will find. It never returns a
// reference it did not successfully write: on any error the caller must abort the
// provision.
func (c *Credentials) Store(ctx context.Context, template, param string, value []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	addr := Address{
		Project:     c.project,
		Environment: c.environment,
		FolderPath:  sanitizeSegment(template),
		Key:         sanitizeSegment(param),
	}
	if addr.FolderPath == "" || addr.Key == "" {
		return "", fmt.Errorf("secretctrl: cannot address a credential for template %q parameter %q", template, param)
	}
	if err := c.client.PutCredential(ctx, addr, value,
		"Provisioned by maintainerd-core for deployment template "+template); err != nil {
		return "", err
	}
	return addr.Ref(), nil
}

// sanitizeSegment keeps a caller-supplied name from escaping the address it was
// given. Template and parameter names reach here from operator input, and a
// segment carrying "/" or ".." would silently write the credential into a
// different folder than the one this policy chose.
func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "/\\") || strings.Contains(s, "..") {
		return ""
	}
	return s
}
