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
	svc := func(name, display, desc, registryKind string) Object {
		return Object{APIVersion, KindService, Meta{name}, ServiceSpec{
			DisplayName: display, Description: desc, Version: "v1",
			// Every capability below is platform-critical, so each mirrors into
			// core's registry as a system service.
			RegistryKind: registryKind, Tier: TierSystem,
		}}
	}
	api := func(name, display, ident string, perms []Permission) Object {
		return Object{APIVersion, KindResourceAPI, Meta{name + "-api"}, ResourceAPISpec{Service: name, Identifier: ident, DisplayName: display, Permissions: perms}}
	}
	// scopes is normally the service's own policy actions: what the credential may
	// ask a token for is exactly what the policy lets it do.
	client := func(name, ident string, scopes []string) Object {
		return Object{APIVersion, KindServiceClient, Meta{name + "-control"}, ServiceClientSpec{Service: name, Audience: ident, AllowedScopes: scopes}}
	}
	policy := func(name string, actions []string) Object {
		return Object{APIVersion, KindServicePolicy, Meta{name + "-service"}, ServicePolicySpec{Service: name, PolicyName: name + "-service", AllowedActions: actions}}
	}
	perm := func(n, d string) Permission { return Permission{Name: n, Description: d} }

	secretAud := aud.AudienceFor("secret")
	runtimeAud := aud.AudienceFor("runtime")
	agentAud := aud.AudienceFor("agent")

	// Outbound grants, declared once and used twice: as the ServicePolicy auth
	// enforces, and as the client's requestable scope allowlist.
	runtimeActions := []string{"secret:GetSecret"}
	agentActions := []string{
		"runtime:Run", "runtime:Stop", "runtime:Restart", "runtime:ReadLogs", "runtime:Exec", "runtime:ReadStats",
		"secret:GetSecret",
	}

	return Catalog{Objects: []Object{
		// --- secret: the KMS-like secret store (a callee) -------------------
		svc("secret", "Maintainerd Secret", "Encrypted secret storage", "Secret"),
		api("secret", "Maintainerd Secret API", secretAud, []Permission{
			perm("secret:GetSecret", "Read a secret value"),
			perm("secret:PutSecret", "Create or update a secret"),
			perm("secret:DeleteSecret", "Delete a secret"),
			perm("secret:ListSecrets", "List secret metadata"),
			perm("secret:RotateSecret", "Rotate a secret"),
			perm("secret:ReadMetadata", "Read secret metadata"),
		}),
		// secret is a pure callee today — it makes no outbound calls, so its policy
		// is empty. The client still needs SOME requestable scope or the credential
		// would be unbounded, so it may request only its own API's audience.
		client("secret", secretAud, []string{secretAud}),
		policy("secret", []string{}),

		// --- runtime: mode-neutral workload operations through the agent ----
		svc("runtime", "Maintainerd Runtime", "Mode-neutral workload operations", "Runtime"),
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
		client("runtime", runtimeAud, runtimeActions),
		// Runtime drivers may pull registry credentials from secret.
		policy("runtime", runtimeActions),

		// --- agent: pulls work from core, drives runtime --------------------
		svc("agent", "Maintainerd Agent", "Workload agent", "Agent"),
		api("agent", "Maintainerd Agent API", agentAud, []Permission{
			perm("agent:register", "Register an agent with core"),
			perm("agent:heartbeat", "Report agent liveness"),
			perm("agent:pull", "Pull work from core"),
			perm("agent:report", "Report work status to core"),
		}),
		client("agent", agentAud, agentActions),
		// the agent operates the runtime and reads the secrets a job needs.
		policy("agent", agentActions),
	}}
}

// AudienceResolver maps a service name to the API audience (aud) its tokens are
// minted for, so audiences come from config rather than being hardcoded.
type AudienceResolver interface {
	AudienceFor(service string) string
}
