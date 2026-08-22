// Package authctrl is core's POST-SETUP control path into auth.
//
// During the setup window core drives auth's SetupService with a bootstrap
// token; CompleteSetup then locks that window permanently. Everything after that
// runs through here: core authenticates to auth as its m2m control client using
// private_key_jwt (RFC 7523) over the private key it minted at setup, and calls
// auth's REGULAR, permission-verified management gRPCs. The resulting token
// carries the `svc` claim, and what core may do is decided by the control policy
// auth granted that principal at setup — not by a bootstrap secret. That is why
// provisioning a new service (say storage) years after install works at all.
//
// # The provisioning boundary — read this before adding an RPC
//
// This package writes ONLY the WIRING a machine can be trusted to converge from
// a declarative catalog:
//
//   - services (service principals)
//   - resource APIs and their permissions
//   - roles and role-permission grants
//   - policies and their attachment to a service
//   - m2m, service-bound clients (private_key_jwt)
//   - workload identity federations
//
// It must NEVER touch identity providers, users, tenant members, security
// settings, branding, or notification templates. Those are auth-console ADMIN
// configuration: they encode an operator's intent about humans and trust
// anchors, they have no representation in the catalog, and a reconcile loop that
// converged them would silently overwrite an administrator's decision on every
// boot — with no operator in the room to notice. If you are reaching for an RPC
// outside the list above, the answer is an auth-console change, not a change
// here.
//
// Corollary: every write in this package is get-or-create and ADDITIVE. Nothing
// here deletes, and nothing here rewrites an object that already exists. A
// second run of the same catalog performs zero writes.
package authctrl
