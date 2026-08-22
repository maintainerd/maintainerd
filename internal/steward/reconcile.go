package steward

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// GeneratedClient is the result of applying a ServiceClient. When the steward
// generates a fresh keypair, PrivateKeyPEM carries the key it must record and
// distribute; on a re-run where the client already existed it is empty.
type GeneratedClient struct {
	ClientID       string
	OAuthClientID  string
	PrivateKeyPEM  string
	AlreadyExisted bool
}

// Applier converges one catalog object into auth. It is the seam between the
// steward (which knows the desired state) and the transport (which knows how to
// talk to auth). In normal operation it is backed by auth's regular,
// permission-verified provisioning gRPCs; those calls are idempotent get-or-
// create, which is what makes reconcile safe to re-run.
type Applier interface {
	EnsureService(ctx context.Context, name string, spec ServiceSpec) error
	EnsureResourceAPI(ctx context.Context, name string, spec ResourceAPISpec) error
	EnsureServiceClient(ctx context.Context, name string, spec ServiceClientSpec) (*GeneratedClient, error)
	EnsureServicePolicy(ctx context.Context, name string, spec ServicePolicySpec) error
}

// KeySink records a service client's freshly generated private key so the
// steward can distribute it to the service (the "core generates + distributes"
// half). Called only when a client's key was newly generated.
type KeySink interface {
	Record(ctx context.Context, service string, privateKeyPEM string) error
}

// Report summarizes a reconcile pass for logging.
type Report struct {
	Services int
	APIs     int
	Clients  int
	Policies int
	NewKeys  int
}

// Reconcile walks the catalog in order and converges every object into auth via
// the Applier. It is ADDITIVE: it creates or updates and never deletes. It stops
// and returns on the first error (a later object may depend on an earlier one),
// so a fixed re-run resumes cleanly.
func Reconcile(ctx context.Context, cat Catalog, a Applier, keys KeySink) (Report, error) {
	var rep Report
	if a == nil {
		return rep, fmt.Errorf("steward: Applier is required")
	}
	for i, obj := range cat.Objects {
		if obj.APIVersion != APIVersion {
			return rep, fmt.Errorf("steward: object %d (%s/%s) has unsupported apiVersion %q", i, obj.Kind, obj.Metadata.Name, obj.APIVersion)
		}
		if obj.Metadata.Name == "" {
			return rep, fmt.Errorf("steward: object %d has empty metadata.name", i)
		}
		if obj.Spec == nil {
			return rep, fmt.Errorf("steward: object %d %q has nil spec", i, obj.Metadata.Name)
		}
		if obj.Kind != obj.Spec.Kind() {
			return rep, fmt.Errorf("steward: object %d %q kind %q does not match spec kind %q", i, obj.Metadata.Name, obj.Kind, obj.Spec.Kind())
		}
		name := obj.Metadata.Name
		switch spec := obj.Spec.(type) {
		case ServiceSpec:
			if err := a.EnsureService(ctx, name, spec); err != nil {
				return rep, fmt.Errorf("steward: ensure Service %q: %w", name, err)
			}
			rep.Services++
		case ResourceAPISpec:
			if err := a.EnsureResourceAPI(ctx, name, spec); err != nil {
				return rep, fmt.Errorf("steward: ensure ResourceAPI %q: %w", name, err)
			}
			rep.APIs++
		case ServiceClientSpec:
			gc, err := a.EnsureServiceClient(ctx, name, spec)
			if err != nil {
				return rep, fmt.Errorf("steward: ensure ServiceClient %q: %w", name, err)
			}
			rep.Clients++
			if gc != nil && gc.PrivateKeyPEM != "" {
				if keys == nil {
					return rep, fmt.Errorf("steward: ServiceClient %q produced a key but no KeySink is configured", name)
				}
				if err := keys.Record(ctx, spec.Service, gc.PrivateKeyPEM); err != nil {
					return rep, fmt.Errorf("steward: record key for %q: %w", spec.Service, err)
				}
				rep.NewKeys++
			}
		case ServicePolicySpec:
			if err := validatePolicyActions(spec.AllowedActions); err != nil {
				return rep, fmt.Errorf("steward: ServicePolicy %q: %w", name, err)
			}
			if err := a.EnsureServicePolicy(ctx, name, spec); err != nil {
				return rep, fmt.Errorf("steward: ensure ServicePolicy %q: %w", name, err)
			}
			rep.Policies++
		default:
			return rep, fmt.Errorf("steward: object %d %q has unknown kind %q", i, name, obj.Kind)
		}
	}
	slog.Info("steward: reconciled control-plane catalog",
		"services", rep.Services, "apis", rep.APIs, "clients", rep.Clients,
		"policies", rep.Policies, "new_keys", rep.NewKeys)
	return rep, nil
}

func validatePolicyActions(actions []string) error {
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action == "" {
			return fmt.Errorf("allowedActions contains an empty action")
		}
		if action == "*" {
			return fmt.Errorf("allowedActions must enumerate grants, not bare *")
		}
	}
	return nil
}
