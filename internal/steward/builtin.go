package steward

// BuiltinCatalog is maintainerd's default control-plane desired state: the
// system services core registers in auth so IAM governs service-to-service
// calls. Every future maintainerd service is added here as four objects
// (Service, ResourceAPI, ServiceClient, ServicePolicy) rather than as new code.
//
// Audiences and the permission/policy taxonomy below are the STARTING set —
// they are data, meant to be tuned. Audiences are resolved from config at
// reconcile time (see AudienceFor); the identifiers here are the defaults.
//
// core itself and the operator console remain provisioned by SetupService during
// the setup window (core needs its control identity before it can drive auth's
// regular provisioning RPCs); they can be folded into this catalog later.
func BuiltinCatalog(aud AudienceResolver) Catalog {
	svc := func(name, display, desc string) Object {
		return Object{APIVersion, KindService, Meta{name}, ServiceSpec{DisplayName: display, Description: desc, Version: "v1"}}
	}
	api := func(name, display, ident string, perms []Permission) Object {
		return Object{APIVersion, KindResourceAPI, Meta{name + "-api"}, ResourceAPISpec{Service: name, Identifier: ident, DisplayName: display, Permissions: perms}}
	}
	client := func(name, ident string) Object {
		return Object{APIVersion, KindServiceClient, Meta{name + "-control"}, ServiceClientSpec{Service: name, Audience: ident}}
	}
	policy := func(name string, actions []string) Object {
		return Object{APIVersion, KindServicePolicy, Meta{name + "-service"}, ServicePolicySpec{Service: name, PolicyName: name + "-service", AllowedActions: actions}}
	}
	perm := func(n, d string) Permission { return Permission{Name: n, Description: d} }

	secretAud := aud.AudienceFor("secret")
	runtimeAud := aud.AudienceFor("runtime")
	agentAud := aud.AudienceFor("agent")

	return Catalog{Objects: []Object{
		// --- secret: the KMS-like secret store (a callee) -------------------
		svc("secret", "Maintainerd Secret", "Encrypted secret storage"),
		api("secret", "Maintainerd Secret API", secretAud, []Permission{
			perm("secret:GetSecret", "Read a secret value"),
			perm("secret:PutSecret", "Create or update a secret"),
			perm("secret:DeleteSecret", "Delete a secret"),
			perm("secret:ListSecrets", "List secret metadata"),
			perm("secret:RotateSecret", "Rotate a secret"),
			perm("secret:ReadMetadata", "Read secret metadata"),
		}),
		client("secret", secretAud),
		// secret is a pure callee today — it needs no outbound grants.
		policy("secret", []string{}),

		// --- runtime: mode-neutral workload operations through the agent ----
		svc("runtime", "Maintainerd Runtime", "Mode-neutral workload operations"),
		api("runtime", "Maintainerd Runtime API", runtimeAud, []Permission{
			perm("runtime:List", "List owned workloads"),
			perm("runtime:Inspect", "Inspect an owned workload"),
			perm("runtime:Run", "Start a workload"),
			perm("runtime:Stop", "Stop a workload"),
			perm("runtime:Restart", "Restart a workload"),
			perm("runtime:ReadLogs", "Read workload logs"),
			perm("runtime:Exec", "Open an exec session in a workload"),
			perm("runtime:ReadStats", "Read workload resource usage"),
		}),
		client("runtime", runtimeAud),
		// Runtime drivers may pull registry credentials from secret.
		policy("runtime", []string{"secret:GetSecret"}),

		// --- agent: pulls work from core, drives runtime --------------------
		svc("agent", "Maintainerd Agent", "Workload agent"),
		api("agent", "Maintainerd Agent API", agentAud, []Permission{
			perm("agent:register", "Register an agent with core"),
			perm("agent:heartbeat", "Report agent liveness"),
			perm("agent:pull", "Pull work from core"),
			perm("agent:report", "Report work status to core"),
		}),
		client("agent", agentAud),
		// the agent operates the runtime and reads the secrets a job needs.
		policy("agent", []string{
			"runtime:Run", "runtime:Stop", "runtime:Restart", "runtime:ReadLogs", "runtime:Exec", "runtime:ReadStats",
			"secret:GetSecret",
		}),
	}}
}

// AudienceResolver maps a service name to the API audience (aud) its tokens are
// minted for, so audiences come from config rather than being hardcoded.
type AudienceResolver interface {
	AudienceFor(service string) string
}
