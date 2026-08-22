// Package supervisor is Core's availability loop.
//
// The platform's hard rule is that system services must never go down
// (plan/12-supervision-and-availability.md). Three separate mechanisms cover the
// tiers of that promise, and this package is exactly one of them:
//
//   - The container runtime (docker `restart: always` / systemd) supervises the
//     SUBSTRATE — core and the agent. It has to: the agent's runtime driver runs
//     inside the agent, so the agent cannot restart itself without killing its
//     own process mid-call, and Core cannot restart itself either. Something
//     dumber than Maintainerd must restart the first process.
//   - The agent supervises system-tier workloads from its on-disk cache, which
//     is what keeps Auth alive while CORE is unreachable.
//   - This loop supervises system-tier workloads while Core IS reachable, and it
//     handles everything a restart policy structurally cannot see: a crash-loop
//     that exhausted its retry budget, a host that stopped answering, and
//     unhealthy-but-running.
//
// Each tick, in order:
//
//	(a) sweep agent liveness  — a host that stopped beating is marked offline
//	(b) flag stranded system workloads on an offline host — never reschedule
//	(c) keep system services alive — re-dispatch, forever, with escalation
//
// Order is load-bearing: (b) and (c) both need to know which hosts are gone, and
// (c) must not fight (b) by re-dispatching work to a host that cannot answer.
//
// Everything here is best-effort and never fatal. A failing supervision pass is
// an ordinary condition (the database blips, setup has not run yet); taking Core
// down because the availability loop had a bad tick would be self-defeating.
package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/agent"
	"github.com/maintainerd/core/internal/event"
	"github.com/maintainerd/core/internal/resource"
	"github.com/maintainerd/core/internal/service"
	"github.com/maintainerd/core/internal/steward"
)

// Defaults for Options. See each field for the reasoning.
const (
	DefaultInterval          = 15 * time.Second
	DefaultAgentOfflineAfter = 90 * time.Second
	DefaultStaleAfter        = 5 * time.Minute
	DefaultEscalateAfter     = 5
)

// systemInstancePrefix is how a system service's registry name maps to its
// resource instance name. It is a CONVENTION shared with the publisher —
// setup.Orchestrator.publishSystemResources creates "system-"+name from
// SYSTEM_AUTH_IMAGE / SYSTEM_SECRET_IMAGE — and the two must move together.
const systemInstancePrefix = "system-"

// runningState is the one state that means "the agent last reported this
// workload up". Everything else (pending, error, failed, exited, unhealthy,
// removed) is a system service that is not serving.
const runningState = "running"

// substrateServices are supervised by the container runtime's restart policy,
// never by Core. This is not a tuning knob; it is the cycle the platform cannot
// break from the inside:
//
//	core   — cannot restart itself.
//	agent  — the runtime driver runs INSIDE the agent, so telling the agent to
//	         restart the agent kills the process mid-call.
//	docker — the engine the driver talks to; it is the substrate, not a workload.
//	runtime— a capability exposed BY the agent, not a separately deployed
//	         workload, so there is nothing for Core to keep alive.
//
// Registry rows for these exist (and are is_system, so they stay undeletable),
// but keep-alive skips them. plan/12-supervision-and-availability.md, decision 2.
var substrateServices = map[string]bool{
	"core":    true,
	"agent":   true,
	"docker":  true,
	"runtime": true,
}

// Options tunes the loop. The bootstrap fills it from the environment; New
// applies the documented default for any non-positive value, so a zero Options
// is a working configuration rather than a loop that never ticks.
type Options struct {
	// Interval (SUPERVISOR_INTERVAL, default 15s) is how often a pass runs.
	Interval time.Duration

	// AgentOfflineAfter (AGENT_OFFLINE_AFTER, default 90s) is how long an agent
	// may go without a heartbeat before it is marked offline. The agent beats
	// every 10s on a goroutine that work execution cannot block, so 90s is nine
	// intervals: well past the three missed beats a transient blip produces,
	// plus slack for a slow round trip. Too low and a GC pause flaps the agent;
	// too high and a dead host reads as online while its services are down.
	AgentOfflineAfter time.Duration

	// StaleAfter (SUPERVISOR_STALE_AFTER, default 5m) is how long a system
	// instance may sit out-of-sync — dispatched and silent, or never picked up —
	// before keep-alive re-dispatches it. It must exceed the dispatch lease and
	// a realistic image pull, or supervision would re-dispatch work that is
	// merely slow.
	StaleAfter time.Duration

	// EscalateAfter (SUPERVISOR_ESCALATE_AFTER, default 5) is how many
	// CONSECUTIVE re-dispatches a system service may take without reaching
	// healthy before an escalation record is written. Re-dispatch continues
	// forever afterwards — escalation says "a human is needed", never "stop".
	EscalateAfter int

	// DeploymentMode (DEPLOYMENT_MODE) is recorded on stranded-workload
	// escalations because the correct remediation differs by substrate: docker
	// mode has no scheduler at all, so a human moves the workload.
	DeploymentMode string
}

func (o Options) withDefaults() Options {
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	if o.AgentOfflineAfter <= 0 {
		o.AgentOfflineAfter = DefaultAgentOfflineAfter
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = DefaultStaleAfter
	}
	if o.EscalateAfter < 1 {
		o.EscalateAfter = DefaultEscalateAfter
	}
	if o.DeploymentMode == "" {
		o.DeploymentMode = "docker"
	}
	return o
}

// Agents is the agent-liveness half of the loop's data contract.
type Agents interface {
	SweepOffline(ctx context.Context, staleAfter time.Duration) ([]agent.Agent, error)
	ListOffline(ctx context.Context) ([]agent.Agent, error)
}

// Resources is the system-tier read plus the only two writes supervision makes.
// Deliberately narrow: the supervisor cannot create, delete or re-spec anything.
type Resources interface {
	ListSystemTier(ctx context.Context) ([]resource.SystemInstance, error)
	RedispatchSystem(ctx context.Context, id uuid.UUID) (*resource.SystemInstance, error)
	FlagHostUnreachable(ctx context.Context, id uuid.UUID) (bool, error)
}

// Registry is the service-registry feed of platform-critical services.
type Registry interface {
	ListSystem(ctx context.Context) ([]service.Registration, error)
}

// Emitter writes the durable escalation record.
type Emitter interface {
	Emit(ctx context.Context, in event.Input) (*event.Event, error)
}

// Tiers resolves a registry service name to the availability tier the CATALOG
// declares for it. steward.Catalog satisfies it.
type Tiers interface {
	ServiceTier(name string) (steward.Tier, bool)
}

// episode is one incident for one supervised service, kept in memory.
//
// It exists only to make escalation fire ONCE per incident rather than once per
// tick. Losing it on restart is acceptable and deliberate: the durable record is
// the platform_events row, and a fresh process re-escalating a still-broken
// service is the correct behaviour, not a duplicate.
type episode struct {
	missingReported bool
	redispatches    int
	escalated       bool
}

// Supervisor is the availability loop. Safe for concurrent use; Tick may be
// called directly (tests, a future on-demand endpoint) as well as by Run.
type Supervisor struct {
	agents    Agents
	resources Resources
	registry  Registry
	events    Emitter
	tiers     Tiers
	opts      Options

	mu       sync.Mutex
	episodes map[string]*episode
}

func New(agents Agents, resources Resources, registry Registry, events Emitter, tiers Tiers, opts Options) *Supervisor {
	return &Supervisor{
		agents:    agents,
		resources: resources,
		registry:  registry,
		events:    events,
		tiers:     tiers,
		opts:      opts.withDefaults(),
		episodes:  map[string]*episode{},
	}
}

// Run ticks until ctx is cancelled. It mirrors the boot loop shape used by the
// steward runner: its own goroutine, a ticker, and no path that ends the
// process.
//
// The first pass runs IMMEDIATELY rather than after one interval, because the
// most likely moment for a system service to be down is right after Core was
// down — waiting a full interval would extend that outage for no reason.
func (s *Supervisor) Run(ctx context.Context) {
	slog.Info("supervisor: started",
		"interval", s.opts.Interval.String(),
		"agent_offline_after", s.opts.AgentOfflineAfter.String(),
		"stale_after", s.opts.StaleAfter.String(),
		"escalate_after", s.opts.EscalateAfter,
		"deployment_mode", s.opts.DeploymentMode,
	)
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			slog.Info("supervisor: stopped")
			return
		case <-ticker.C:
		}
	}
}

// Tick runs one supervision pass.
//
// The recover is not defensive clutter: this runs on its own goroutine, and an
// unrecovered panic there takes the whole process down — so a bug in the loop
// that is meant to keep the platform up would be the thing that stops it.
func (s *Supervisor) Tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("supervisor: pass panicked — the loop continues", "panic", r)
		}
	}()
	if ctx.Err() != nil {
		return
	}

	offline := s.sweepAgents(ctx)

	instances, err := s.resources.ListSystemTier(ctx)
	if err != nil {
		slog.Warn("supervisor: system-tier read failed — skipping this pass", "err", err)
		return
	}

	s.flagStranded(ctx, instances, offline)
	s.keepAlive(ctx, instances, offline)
}

// --- (a) agent liveness -----------------------------------------------------

// sweepAgents marks agents past the heartbeat threshold offline and returns the
// STANDING offline set keyed by internal id (what (b) and (c) need).
//
// Transitions and the standing set are used for different jobs: a transition is
// news and gets one event; the standing set is the current topology. A failed
// sweep returns an empty set rather than aborting the pass — keep-alive on a
// possibly-stale view still beats no keep-alive.
func (s *Supervisor) sweepAgents(ctx context.Context) map[int64]agent.Agent {
	transitioned, err := s.agents.SweepOffline(ctx, s.opts.AgentOfflineAfter)
	if err != nil {
		slog.Warn("supervisor: agent liveness sweep failed", "err", err)
	}
	for _, a := range transitioned {
		slog.Warn("supervisor: agent marked offline — missed heartbeat threshold",
			"agent", a.UUID, "name", a.Name, "last_seen_at", lastSeen(a),
			"offline_after", s.opts.AgentOfflineAfter.String())
		s.emit(ctx, event.Input{
			Kind:        event.KindAgentOffline,
			Severity:    event.SeverityWarning,
			SubjectType: event.SubjectAgent,
			SubjectUUID: uuidPtr(a.UUID),
			Message: fmt.Sprintf("agent %q stopped reporting; no heartbeat for more than %s",
				a.Name, s.opts.AgentOfflineAfter),
			Details: map[string]any{
				"agent_uuid":    a.UUID.String(),
				"agent_name":    a.Name,
				"last_seen_at":  lastSeen(a),
				"offline_after": s.opts.AgentOfflineAfter.String(),
			},
		})
	}

	standing, err := s.agents.ListOffline(ctx)
	if err != nil {
		slog.Warn("supervisor: offline-agent read failed", "err", err)
		return map[int64]agent.Agent{}
	}
	out := make(map[int64]agent.Agent, len(standing))
	for _, a := range standing {
		out[a.ID] = a
	}
	return out
}

// --- (b) stranded system workloads ------------------------------------------

// flagStranded records that a system-tier workload's host has gone away.
//
// It never touches the agent assignment. In docker mode there is no scheduler to
// reschedule onto and the workload's data may be host-local, so silently moving
// it risks two hosts running the same stateful system service the moment the
// original comes back — a worse failure than the outage. Core surfaces the fact
// and escalates; a human or an explicit policy decides
// (plan/12-supervision-and-availability.md, decision 8).
//
// NON-system workloads on an offline agent are deliberately absent from this
// pass: their dispatch lease simply expires and the ordinary feed re-offers them
// to whichever agent pulls next. That is the correct blast radius — a tenant
// workload is not worth guessing about.
func (s *Supervisor) flagStranded(ctx context.Context, instances []resource.SystemInstance, offline map[int64]agent.Agent) {
	for _, inst := range instances {
		if inst.AgentID == 0 || inst.Terminating() {
			continue
		}
		host, gone := offline[inst.AgentID]
		if !gone {
			continue
		}
		flagged, err := s.resources.FlagHostUnreachable(ctx, inst.UUID)
		if err != nil {
			slog.Warn("supervisor: flagging stranded system workload failed",
				"resource", inst.UUID, "name", inst.Name, "err", err)
			continue
		}
		if !flagged {
			continue // already flagged earlier in this episode
		}
		slog.Error("supervisor: system workload stranded on an offline host — NOT rescheduled",
			"resource", inst.UUID, "name", inst.Name, "agent", host.UUID,
			"deployment_mode", s.opts.DeploymentMode)
		s.emit(ctx, event.Input{
			Kind:        event.KindSystemHostUnreachable,
			Severity:    event.SeverityCritical,
			SubjectType: event.SubjectResource,
			SubjectUUID: uuidPtr(inst.UUID),
			Message: fmt.Sprintf(
				"system workload %q is stranded: its agent %q is offline. It was NOT rescheduled — "+
					"there is no scheduler in %s mode and the workload's data may be host-local, "+
					"so recovering the host or reassigning the workload is an operator decision.",
				inst.Name, host.Name, s.opts.DeploymentMode),
			Details: map[string]any{
				"resource_uuid":   inst.UUID.String(),
				"resource_name":   inst.Name,
				"agent_uuid":      host.UUID.String(),
				"agent_name":      host.Name,
				"agent_last_seen": lastSeen(host),
				"deployment_mode": s.opts.DeploymentMode,
				"rescheduled":     false,
			},
		})
	}
}

// --- (c)/(d) system-service keep-alive + escalation -------------------------

// keepAlive walks the system services the registry says are platform-critical
// and makes sure each one's instance is actually being driven toward running.
func (s *Supervisor) keepAlive(ctx context.Context, instances []resource.SystemInstance, offline map[int64]agent.Agent) {
	registrations, err := s.registry.ListSystem(ctx)
	if err != nil {
		slog.Warn("supervisor: system-service registry read failed — skipping keep-alive", "err", err)
		return
	}

	byName := make(map[string]resource.SystemInstance, len(instances))
	for _, inst := range instances {
		byName[inst.Name] = inst
	}

	live := make(map[string]bool, len(registrations))
	for _, reg := range registrations {
		name := strings.ToLower(strings.TrimSpace(reg.Name))
		if name == "" || !s.supervises(name) {
			continue
		}
		key := "service:" + name
		live[key] = true

		instanceName := systemInstancePrefix + name
		inst, ok := byName[instanceName]
		if !ok {
			s.reportMissingInstance(ctx, key, reg, instanceName)
			continue
		}
		s.clearMissing(key, reg.Name, instanceName)

		if inst.Terminating() {
			// An operator asked for this to go away. Keep-alive stands down and
			// forgets the incident — the teardown is the desired state.
			s.forget(key)
			continue
		}
		if host, gone := offline[inst.AgentID]; inst.AgentID != 0 && gone {
			// Nothing to dispatch to: the item is stickily assigned to this
			// agent, so re-dispatching would only churn generation and inflate
			// the escalation counter. flagStranded already raised the alarm.
			slog.Warn("supervisor: keep-alive deferred — the owning host is offline",
				"resource", inst.UUID, "name", inst.Name, "agent", host.UUID)
			continue
		}

		reason := s.redispatchReason(inst)
		if reason == "" {
			s.noteRecovered(ctx, key, inst)
			continue
		}
		s.redispatch(ctx, key, inst, reason)
	}
	s.prune(live)
}

// supervises decides whether Core keeps this registry service alive.
//
// The catalog is consulted first because availability tier is REGISTRATION data
// (decision 5): a service the catalog declares TierApp is explicitly not
// platform-critical and is left to the ordinary deployment loop. Catalog silence
// is not an opt-out, though — core and auth are provisioned by SetupService and
// never appear in the catalog — so an is_system registry row the catalog has
// never heard of is still supervised. is_system is already the flag that makes a
// row undeletable; reading it as "must run" is the same claim.
//
// The substrate exclusion is checked first and unconditionally: those four can
// never be supervised by Core, whatever any registration says.
func (s *Supervisor) supervises(name string) bool {
	if substrateServices[name] {
		return false
	}
	if tier, declared := s.tiers.ServiceTier(name); declared {
		return tier == steward.TierSystem
	}
	return true
}

// redispatchReason returns why this instance needs re-dispatching, or "" when it
// is fine. The order encodes the priorities.
func (s *Supervisor) redispatchReason(inst resource.SystemInstance) string {
	switch {
	case inst.Health == resource.HealthUnhealthy:
		// The whole point of promoting health to a column: the process is up and
		// the service still does not work.
		return "the agent reports health=unhealthy (running is not working)"

	case inst.Health == resource.HealthUnknown:
		// Core wrote 'unknown' when the host went away and nothing has reported
		// since. For a service that must never go down, "we do not know" is
		// treated as "not running" — re-dispatch is a converge, not a recreate,
		// so acting on a false alarm is cheap.
		return "health is unknown since the reporting host went offline"

	case inst.State == "failed":
		// The retry budget parked it. That budget exists to stop a poisoned
		// TENANT spec from hot-looping an agent; a system service may never be
		// parked as terminally failed, so this is un-parked on sight rather than
		// after a wait.
		return "parked as terminally 'failed' — a system service is retried forever"

	case inst.State == runningState && inst.Converged():
		return ""

	case inst.Converged():
		// The agent answered for the current revision and the answer is not
		// "running" (exited, error, unhealthy, or removed without anyone asking).
		return "the last agent report says state=" + inst.State

	case time.Since(inst.UpdatedAt) > s.opts.StaleAfter:
		// Dispatched and silent, or never picked up at all.
		return fmt.Sprintf("out of sync and silent for %s (generation %d, observed %d)",
			time.Since(inst.UpdatedAt).Truncate(time.Second), inst.Generation, inst.ObservedGeneration)
	}
	// Out of sync but still within the stale window: the ordinary feed owns it.
	return ""
}

func (s *Supervisor) redispatch(ctx context.Context, key string, inst resource.SystemInstance, reason string) {
	if _, err := s.resources.RedispatchSystem(ctx, inst.UUID); err != nil {
		slog.Warn("supervisor: re-dispatching a system instance failed",
			"resource", inst.UUID, "name", inst.Name, "reason", reason, "err", err)
		return
	}

	s.mu.Lock()
	ep := s.episodeLocked(key)
	ep.redispatches++
	count := ep.redispatches
	escalate := count > s.opts.EscalateAfter && !ep.escalated
	if escalate {
		ep.escalated = true
	}
	s.mu.Unlock()

	slog.Warn("supervisor: system instance re-dispatched",
		"resource", inst.UUID, "name", inst.Name, "reason", reason,
		"consecutive_redispatches", count, "state", inst.State, "health", inst.Health)

	if !escalate {
		return
	}
	slog.Error("supervisor: system service is not recovering — escalating",
		"resource", inst.UUID, "name", inst.Name,
		"consecutive_redispatches", count, "escalate_after", s.opts.EscalateAfter)
	s.emit(ctx, event.Input{
		Kind:        event.KindSystemRedispatchEscalated,
		Severity:    event.SeverityCritical,
		SubjectType: event.SubjectResource,
		SubjectUUID: uuidPtr(inst.UUID),
		Message: fmt.Sprintf(
			"system service %q has been re-dispatched %d consecutive times without reaching healthy (%s). "+
				"Core keeps retrying — it never gives up on a system service — but the cause needs a human.",
			inst.Name, count, reason),
		Details: map[string]any{
			"resource_uuid":            inst.UUID.String(),
			"resource_name":            inst.Name,
			"consecutive_redispatches": count,
			"escalate_after":           s.opts.EscalateAfter,
			"state":                    inst.State,
			"health":                   inst.Health,
			"reason":                   reason,
			"attempts":                 inst.Attempts,
		},
	})
}

// noteRecovered clears the episode for an instance that is running and
// converged, and closes an escalation it had opened so the log records the end
// of the incident and not just its start. (Named to stay clear of the builtin
// recover used in Tick.)
func (s *Supervisor) noteRecovered(ctx context.Context, key string, inst resource.SystemInstance) {
	s.mu.Lock()
	ep, ok := s.episodes[key]
	wasEscalated := ok && ep.escalated
	redispatches := 0
	if ok {
		redispatches = ep.redispatches
		ep.redispatches = 0
		ep.escalated = false
	}
	s.mu.Unlock()

	if !wasEscalated {
		return
	}
	slog.Info("supervisor: system service recovered",
		"resource", inst.UUID, "name", inst.Name, "redispatches", redispatches)
	s.emit(ctx, event.Input{
		Kind:        event.KindSystemRecovered,
		Severity:    event.SeverityInfo,
		SubjectType: event.SubjectResource,
		SubjectUUID: uuidPtr(inst.UUID),
		Message: fmt.Sprintf("system service %q is running and converged again after %d re-dispatches",
			inst.Name, redispatches),
		Details: map[string]any{
			"resource_uuid": inst.UUID.String(),
			"resource_name": inst.Name,
			"redispatches":  redispatches,
			"health":        inst.Health,
		},
	})
}

// reportMissingInstance records that a platform-critical service has no
// desired-state instance to reconcile.
//
// Core deliberately does NOT fabricate a spec here. Which image, tag and
// arguments a system service runs is publishing input (SYSTEM_AUTH_IMAGE /
// SYSTEM_SECRET_IMAGE, applied by setup's system-tier publishing) — a supervisor
// that invented one would silently deploy something nobody declared, and a wrong
// guess for Auth is worse than a precise gap report.
//
// Reported once per episode: at a 15s interval, logging this every tick would
// bury the incident it is meant to surface.
func (s *Supervisor) reportMissingInstance(ctx context.Context, key string, reg service.Registration, instanceName string) {
	s.mu.Lock()
	ep := s.episodeLocked(key)
	first := !ep.missingReported
	ep.missingReported = true
	s.mu.Unlock()
	if !first {
		return
	}
	slog.Error("supervisor: system service has no desired-state instance — nothing to keep alive",
		"service", reg.Name, "kind", reg.Kind, "service_uuid", reg.UUID,
		"expected_resource_name", instanceName)
	s.emit(ctx, event.Input{
		Kind:        event.KindSystemInstanceMissing,
		Severity:    event.SeverityCritical,
		SubjectType: event.SubjectService,
		SubjectUUID: uuidPtr(reg.UUID),
		Message: fmt.Sprintf(
			"system service %q is registered as platform-critical but has no system-tier resource named %q, "+
				"so there is nothing for Core to keep alive. Publish its workload (SYSTEM_%s_IMAGE) — "+
				"Core will not invent a spec for a system service.",
			reg.Name, instanceName, strings.ToUpper(reg.Name)),
		Details: map[string]any{
			"service_uuid":           reg.UUID.String(),
			"service_name":           reg.Name,
			"service_kind":           reg.Kind,
			"expected_resource_name": instanceName,
		},
	})
}

// clearMissing closes a previously reported gap once the instance appears.
func (s *Supervisor) clearMissing(key, serviceName, instanceName string) {
	s.mu.Lock()
	ep, ok := s.episodes[key]
	closed := ok && ep.missingReported
	if ok {
		ep.missingReported = false
	}
	s.mu.Unlock()
	if closed {
		slog.Info("supervisor: system service now has a desired-state instance",
			"service", serviceName, "resource_name", instanceName)
	}
}

// --- episode bookkeeping ----------------------------------------------------

// episodeLocked returns (creating if needed) the episode for key. Callers hold
// s.mu.
func (s *Supervisor) episodeLocked(key string) *episode {
	ep, ok := s.episodes[key]
	if !ok {
		ep = &episode{}
		s.episodes[key] = ep
	}
	return ep
}

func (s *Supervisor) forget(key string) {
	s.mu.Lock()
	delete(s.episodes, key)
	s.mu.Unlock()
}

// prune drops episodes for services that are no longer supervised (deregistered,
// retiered, or torn down), so the map cannot grow for the life of the process.
func (s *Supervisor) prune(live map[string]bool) {
	s.mu.Lock()
	for key := range s.episodes {
		if !live[key] {
			delete(s.episodes, key)
		}
	}
	s.mu.Unlock()
}

// --- helpers ----------------------------------------------------------------

// emit records an event, treating failure as non-fatal. Losing the record of an
// incident is bad; letting the write that records it abort the loop that is
// fixing the incident is worse.
func (s *Supervisor) emit(ctx context.Context, in event.Input) {
	if s.events == nil {
		return
	}
	if _, err := s.events.Emit(ctx, in); err != nil {
		slog.Warn("supervisor: recording a platform event failed", "kind", in.Kind, "err", err)
	}
}

func uuidPtr(id uuid.UUID) *uuid.UUID { return &id }

// lastSeen renders an agent's last heartbeat for logs and event details, without
// pretending a never-seen agent was seen at the zero time.
func lastSeen(a agent.Agent) string {
	if a.LastSeenAt == nil {
		return "never"
	}
	return a.LastSeenAt.UTC().Format(time.RFC3339)
}
