// Package steward is maintainerd's control-plane reconciler. It holds the
// desired state of the control plane as a declarative catalog of typed objects:
// a Kubernetes-style apiVersion/kind/metadata/spec model, but scoped to
// maintainerd's own services and IAM, never real cloud infra. A transport
// adapter converges auth to match it by driving auth's regular, permission-
// verified provisioning RPCs.
//
// It is additive by design: reconcile creates or updates every object in the
// catalog and never deletes. Because the underlying RPCs are get-or-create, a
// reconcile is re-runnable and self-healing — core can run it on every boot and
// auth ends up in the catalog's shape regardless of where a prior run stopped.
package steward

// APIVersion namespaces the catalog schema so it can evolve independently, the
// way a Kubernetes apiVersion does. Bump the version, not the meaning of v1.
const APIVersion = "control.maintainerd/v1"

// Kind is the type of a control-plane object. New maintainerd capabilities add
// new kinds here rather than new bespoke provisioning code.
type Kind string

const (
	KindService       Kind = "Service"       // a service principal auth governs
	KindResourceAPI   Kind = "ResourceAPI"   // an API a service protects + its permissions
	KindServiceClient Kind = "ServiceClient" // a service's M2M client (steward-generated key)
	KindServicePolicy Kind = "ServicePolicy" // the cross-service actions a service may perform
)

// Meta identifies an object within its kind.
type Meta struct {
	Name string `json:"name" yaml:"name"`
}

// Spec is the kind-specific body of an object; each concrete spec reports its Kind.
type Spec interface{ Kind() Kind }

// Object is one declarative entry in the catalog — maintainerd's analog of a
// Kubernetes manifest object.
type Object struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind   `json:"kind" yaml:"kind"`
	Metadata   Meta   `json:"metadata" yaml:"metadata"`
	Spec       Spec   `json:"spec" yaml:"spec"`
}

// Catalog is the ordered desired state of the control plane. Order is
// significant: a ResourceAPI must exist before a ServiceClient bound to it, and
// a ServicePolicy references permissions a ResourceAPI defines — so reconcile
// applies objects in slice order.
type Catalog struct {
	Objects []Object
}

// ---- specs -----------------------------------------------------------------

// ServiceSpec declares a service principal: a maintainerd service (secret,
// runtime, agent, etc.) that auth can authorize calls to and/or from.
type ServiceSpec struct {
	DisplayName string `json:"displayName" yaml:"displayName"`
	Description string `json:"description" yaml:"description"`
	Version     string `json:"version" yaml:"version"`
}

func (ServiceSpec) Kind() Kind { return KindService }

// ResourceAPISpec declares the API a service protects and the permissions it
// defines. Identifier is the audience (aud) tokens for this API carry.
type ResourceAPISpec struct {
	Service     string       `json:"service" yaml:"service"`
	Identifier  string       `json:"identifier" yaml:"identifier"`
	DisplayName string       `json:"displayName" yaml:"displayName"`
	Permissions []Permission `json:"permissions" yaml:"permissions"`
}

func (ResourceAPISpec) Kind() Kind { return KindResourceAPI }

// Permission is one action an API defines (e.g. "secret:GetSecret").
type Permission struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
}

// ServiceClientSpec declares a service's machine (M2M) client. The steward
// generates the keypair, registers only the PUBLIC JWKS with auth, and records +
// distributes the private key (auth never holds a credential that could
// impersonate the service).
type ServiceClientSpec struct {
	Service  string `json:"service" yaml:"service"`
	Audience string `json:"audience" yaml:"audience"`
}

func (ServiceClientSpec) Kind() Kind { return KindServiceClient }

// ServicePolicySpec declares the exact cross-service actions a service may
// perform, attached to its principal — the authorization half. A bare "*" is
// refused upstream; grants are enumerated.
type ServicePolicySpec struct {
	Service        string   `json:"service" yaml:"service"`
	PolicyName     string   `json:"policyName" yaml:"policyName"`
	AllowedActions []string `json:"allowedActions" yaml:"allowedActions"`
}

func (ServicePolicySpec) Kind() Kind { return KindServicePolicy }
