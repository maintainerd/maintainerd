package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/agent"
	"github.com/maintainerd/core/internal/event"
	"github.com/maintainerd/core/internal/resource"
	"github.com/maintainerd/core/internal/service"
	"github.com/maintainerd/core/internal/steward"
)

// --- fakes -----------------------------------------------------------------

// fakeAgents mirrors the sweeper's contract: SweepOffline returns TRANSITIONS
// only, ListOffline returns the standing set.
type fakeAgents struct {
	transitions []agent.Agent
	standing    []agent.Agent
	sweptWith   time.Duration
	sweeps      int
	sweepErr    error
	standingErr error
	// swept signals each pass, for the tests that drive Run concurrently.
	swept chan struct{}
}

func (f *fakeAgents) SweepOffline(_ context.Context, staleAfter time.Duration) ([]agent.Agent, error) {
	f.sweeps++
	f.sweptWith = staleAfter
	if f.swept != nil {
		select {
		case f.swept <- struct{}{}:
		default:
		}
	}
	if f.sweepErr != nil {
		return nil, f.sweepErr
	}
	// Transitions are news exactly once, like the SQL's status <> 'offline'.
	out := f.transitions
	f.transitions = nil
	return out, nil
}

func (f *fakeAgents) ListOffline(context.Context) ([]agent.Agent, error) {
	if f.standingErr != nil {
		return nil, f.standingErr
	}
	return f.standing, nil
}

type fakeResources struct {
	instances []resource.SystemInstance
	listErr   error

	redispatched []uuid.UUID
	flagged      []uuid.UUID
	// alreadyFlagged models the SQL's idempotency guard (second call = no row).
	alreadyFlagged map[uuid.UUID]bool
	redispatchErr  error
}

func (f *fakeResources) ListSystemTier(context.Context) ([]resource.SystemInstance, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.instances, nil
}

func (f *fakeResources) RedispatchSystem(_ context.Context, id uuid.UUID) (*resource.SystemInstance, error) {
	if f.redispatchErr != nil {
		return nil, f.redispatchErr
	}
	f.redispatched = append(f.redispatched, id)
	for i := range f.instances {
		if f.instances[i].UUID == id {
			f.instances[i].Generation++
			f.instances[i].State = "pending"
			f.instances[i].Attempts = 0
			f.instances[i].Leased = false
			inst := f.instances[i]
			return &inst, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeResources) FlagHostUnreachable(_ context.Context, id uuid.UUID) (bool, error) {
	if f.alreadyFlagged == nil {
		f.alreadyFlagged = map[uuid.UUID]bool{}
	}
	if f.alreadyFlagged[id] {
		return false, nil
	}
	f.alreadyFlagged[id] = true
	f.flagged = append(f.flagged, id)
	return true, nil
}

type fakeRegistry struct {
	rows []service.Registration
	err  error
}

func (f *fakeRegistry) ListSystem(context.Context) ([]service.Registration, error) {
	return f.rows, f.err
}

type fakeEmitter struct {
	emitted []event.Input
	err     error
}

func (f *fakeEmitter) Emit(_ context.Context, in event.Input) (*event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.emitted = append(f.emitted, in)
	return &event.Event{Kind: in.Kind}, nil
}

func (f *fakeEmitter) kinds() []string {
	out := make([]string, 0, len(f.emitted))
	for _, e := range f.emitted {
		out = append(out, e.Kind)
	}
	return out
}

func (f *fakeEmitter) count(kind string) int {
	n := 0
	for _, e := range f.emitted {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// fakeTiers stands in for the catalog. A name absent from the map is catalog
// SILENCE (the auth/core case), which is not an opt-out of supervision.
type fakeTiers struct{ declared map[string]steward.Tier }

func (f fakeTiers) ServiceTier(name string) (steward.Tier, bool) {
	t, ok := f.declared[name]
	return t, ok
}

// --- helpers ---------------------------------------------------------------

func sup(t *testing.T, agents *fakeAgents, res *fakeResources, reg *fakeRegistry, em *fakeEmitter, tiers Tiers, opts Options) *Supervisor {
	t.Helper()
	if tiers == nil {
		tiers = fakeTiers{}
	}
	return New(agents, res, reg, em, tiers, opts)
}

func systemService(name, kind string) service.Registration {
	return service.Registration{UUID: uuid.New(), Name: name, Kind: kind, IsSystem: true}
}

func offlineAgent(id int64, name string, lastSeen time.Duration) agent.Agent {
	seen := time.Now().Add(-lastSeen)
	return agent.Agent{ID: id, UUID: uuid.New(), Name: name, Status: "offline", LastSeenAt: &seen}
}

// instance builds a system-tier instance. agentID 0 = unassigned.
func instance(name, state, health string, agentID int64, gen, observed int64, age time.Duration) resource.SystemInstance {
	return resource.SystemInstance{
		UUID:               uuid.New(),
		Name:               name,
		Kind:               "Workload",
		State:              state,
		Health:             health,
		AgentID:            agentID,
		Generation:         gen,
		ObservedGeneration: observed,
		UpdatedAt:          time.Now().Add(-age),
		Metadata:           map[string]any{"tier": "system"},
	}
}

// healthyAuth is the steady state: running, converged, healthy.
func healthyAuth() resource.SystemInstance {
	return instance("system-auth", "running", resource.HealthHealthy, 1, 3, 3, time.Second)
}

// --- (a) liveness ----------------------------------------------------------

func TestTickSweepsWithTheConfiguredThreshold(t *testing.T) {
	agents := &fakeAgents{transitions: []agent.Agent{offlineAgent(1, "host-1", 5*time.Minute)}}
	em := &fakeEmitter{}
	s := sup(t, agents, &fakeResources{}, &fakeRegistry{}, em, nil,
		Options{AgentOfflineAfter: 42 * time.Second})

	s.Tick(context.Background())

	assert.Equal(t, 42*time.Second, agents.sweptWith)
	assert.Equal(t, []string{event.KindAgentOffline}, em.kinds())
	assert.Equal(t, event.SubjectAgent, em.emitted[0].SubjectType)
	assert.Equal(t, event.SeverityWarning, em.emitted[0].Severity)
}

func TestTickEmitsOneEventPerOfflineTransition(t *testing.T) {
	// The standing set keeps reporting the host, but only the TRANSITION is news.
	host := offlineAgent(1, "host-1", 5*time.Minute)
	agents := &fakeAgents{transitions: []agent.Agent{host}, standing: []agent.Agent{host}}
	em := &fakeEmitter{}
	s := sup(t, agents, &fakeResources{}, &fakeRegistry{}, em, nil, Options{})

	s.Tick(context.Background())
	s.Tick(context.Background())
	s.Tick(context.Background())

	assert.Equal(t, 3, agents.sweeps)
	assert.Equal(t, 1, em.count(event.KindAgentOffline))
}

func TestTickSurvivesASweepFailure(t *testing.T) {
	// A failing sweep must not abort the pass: keep-alive on a possibly-stale
	// view still beats no keep-alive.
	agents := &fakeAgents{sweepErr: errors.New("db blip")}
	res := &fakeResources{instances: []resource.SystemInstance{
		instance("system-auth", "failed", resource.HealthUnreported, 1, 2, 1, time.Minute),
	}}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	s := sup(t, agents, res, reg, &fakeEmitter{}, nil, Options{})

	s.Tick(context.Background())

	assert.Len(t, res.redispatched, 1, "keep-alive must still run")
}

func TestTickSkipsThePassWhenTheSystemTierReadFails(t *testing.T) {
	res := &fakeResources{listErr: errors.New("db down")}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	s := sup(t, &fakeAgents{}, res, reg, &fakeEmitter{}, nil, Options{})

	s.Tick(context.Background())

	assert.Empty(t, res.redispatched, "without the instance list there is nothing safe to decide")
}

// --- (b) stranded system workloads -----------------------------------------

func TestStrandedSystemWorkloadIsFlaggedEscalatedAndNeverReassigned(t *testing.T) {
	host := offlineAgent(7, "host-7", 10*time.Minute)
	stranded := instance("system-auth", "running", resource.HealthHealthy, host.ID, 2, 2, time.Minute)
	res := &fakeResources{instances: []resource.SystemInstance{stranded}}
	agents := &fakeAgents{transitions: []agent.Agent{host}, standing: []agent.Agent{host}}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	em := &fakeEmitter{}
	s := sup(t, agents, res, reg, em, nil, Options{DeploymentMode: "docker"})

	s.Tick(context.Background())

	require.Equal(t, []uuid.UUID{stranded.UUID}, res.flagged)
	require.Equal(t, 1, em.count(event.KindSystemHostUnreachable))

	var ev event.Input
	for _, e := range em.emitted {
		if e.Kind == event.KindSystemHostUnreachable {
			ev = e
		}
	}
	assert.Equal(t, event.SeverityCritical, ev.Severity)
	assert.Equal(t, event.SubjectResource, ev.SubjectType)
	require.NotNil(t, ev.SubjectUUID)
	assert.Equal(t, stranded.UUID, *ev.SubjectUUID)
	assert.Equal(t, false, ev.Details["rescheduled"], "docker mode never silently reschedules")
	assert.Equal(t, "docker", ev.Details["deployment_mode"])
	assert.Equal(t, host.UUID.String(), ev.Details["agent_uuid"])

	// The workload keeps its host: agent_id is never rewritten here.
	assert.Equal(t, host.ID, res.instances[0].AgentID)
	// And keep-alive stands down rather than churning generation on a dead host.
	assert.Empty(t, res.redispatched)
}

func TestStrandedEscalationFiresOncePerEpisode(t *testing.T) {
	host := offlineAgent(7, "host-7", 10*time.Minute)
	stranded := instance("system-auth", "running", resource.HealthHealthy, host.ID, 2, 2, time.Minute)
	res := &fakeResources{instances: []resource.SystemInstance{stranded}}
	agents := &fakeAgents{standing: []agent.Agent{host}}
	s := sup(t, agents, res, &fakeRegistry{}, &fakeEmitter{}, nil, Options{})

	for i := 0; i < 4; i++ {
		s.Tick(context.Background())
	}
	assert.Len(t, res.flagged, 1, "the flag's idempotency is the de-duplication")
}

func TestNonSystemWorkloadsAndTeardownsAreLeftAlone(t *testing.T) {
	host := offlineAgent(7, "host-7", 10*time.Minute)
	// Only system-tier rows reach the supervisor at all (the query filters by
	// tier), so the cases it must skip itself are teardowns and unassigned rows.
	res := &fakeResources{instances: []resource.SystemInstance{
		instance("system-secret", "deleting", resource.HealthHealthy, host.ID, 2, 1, time.Hour),
		instance("system-auth", "running", resource.HealthHealthy, 0, 2, 2, time.Hour),
	}}
	agents := &fakeAgents{standing: []agent.Agent{host}}
	s := sup(t, agents, res, &fakeRegistry{}, &fakeEmitter{}, nil, Options{})

	s.Tick(context.Background())

	assert.Empty(t, res.flagged)
}

// --- (c) keep-alive --------------------------------------------------------

func TestKeepAliveRedispatchTriggers(t *testing.T) {
	tests := []struct {
		name         string
		inst         resource.SystemInstance
		staleAfter   time.Duration
		wantRedisp   bool
		wantReasonIn string
	}{
		{
			name:         "running but unhealthy",
			inst:         instance("system-auth", "running", resource.HealthUnhealthy, 1, 2, 2, time.Second),
			wantRedisp:   true,
			wantReasonIn: "unhealthy",
		},
		{
			name:         "health unknown after the host went away",
			inst:         instance("system-auth", "running", resource.HealthUnknown, 1, 2, 2, time.Second),
			wantRedisp:   true,
			wantReasonIn: "unknown",
		},
		{
			name:         "parked as terminally failed is un-parked on sight",
			inst:         instance("system-auth", "failed", resource.HealthUnreported, 1, 2, 1, time.Second),
			wantRedisp:   true,
			wantReasonIn: "failed",
		},
		{
			name:         "reported not-running",
			inst:         instance("system-auth", "exited", resource.HealthNone, 1, 2, 2, time.Second),
			wantRedisp:   true,
			wantReasonIn: "state=exited",
		},
		{
			name:         "removed without anyone asking",
			inst:         instance("system-auth", "removed", resource.HealthUnreported, 1, 2, 2, time.Second),
			wantRedisp:   true,
			wantReasonIn: "state=removed",
		},
		{
			name:         "leased and silent past the stale window",
			inst:         instance("system-auth", "pending", resource.HealthUnreported, 1, 3, 2, 10*time.Minute),
			staleAfter:   5 * time.Minute,
			wantRedisp:   true,
			wantReasonIn: "silent",
		},
		{
			name:       "out of sync but still inside the stale window is the feed's job",
			inst:       instance("system-auth", "pending", resource.HealthUnreported, 1, 3, 2, 30*time.Second),
			staleAfter: 5 * time.Minute,
			wantRedisp: false,
		},
		{
			name:       "running, converged and healthy is left alone",
			inst:       healthyAuth(),
			wantRedisp: false,
		},
		{
			name:       "running with no healthcheck configured is left alone",
			inst:       instance("system-auth", "running", resource.HealthNone, 1, 2, 2, time.Second),
			wantRedisp: false,
		},
		{
			name:       "still starting is left alone",
			inst:       instance("system-auth", "running", resource.HealthStarting, 1, 2, 2, time.Second),
			wantRedisp: false,
		},
		{
			name:       "a requested teardown is never resurrected",
			inst:       instance("system-auth", "deleting", resource.HealthHealthy, 1, 3, 2, time.Hour),
			wantRedisp: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &fakeResources{instances: []resource.SystemInstance{tt.inst}}
			reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
			s := sup(t, &fakeAgents{}, res, reg, &fakeEmitter{}, nil,
				Options{StaleAfter: tt.staleAfter})

			s.Tick(context.Background())

			if !tt.wantRedisp {
				assert.Empty(t, res.redispatched)
				return
			}
			require.Equal(t, []uuid.UUID{tt.inst.UUID}, res.redispatched)
			assert.Contains(t, s.redispatchReason(tt.inst), tt.wantReasonIn)
		})
	}
}

func TestKeepAliveClearsLeaseAndBudgetOnRedispatch(t *testing.T) {
	inst := instance("system-auth", "failed", resource.HealthUnreported, 1, 3, 1, time.Minute)
	inst.Leased = true
	inst.Attempts = 10
	res := &fakeResources{instances: []resource.SystemInstance{inst}}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	s := sup(t, &fakeAgents{}, res, reg, &fakeEmitter{}, nil, Options{})

	s.Tick(context.Background())

	require.Len(t, res.redispatched, 1)
	got := res.instances[0]
	assert.Equal(t, int64(4), got.Generation, "the existing feed is re-armed by the generation bump")
	assert.Equal(t, "pending", got.State)
	assert.False(t, got.Leased)
	assert.Equal(t, int32(0), got.Attempts)
}

func TestASystemServiceIsNeverLeftTerminallyFailed(t *testing.T) {
	// The retry budget may park a tenant workload forever. A system service must
	// come back off 'failed' on every pass, indefinitely — that is the whole rule.
	inst := instance("system-auth", "failed", resource.HealthUnreported, 1, 2, 1, time.Second)
	inst.Attempts = 99
	res := &fakeResources{instances: []resource.SystemInstance{inst}}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	s := sup(t, &fakeAgents{}, res, reg, &fakeEmitter{}, nil, Options{EscalateAfter: 2})

	for i := 0; i < 6; i++ {
		// Re-park it each pass, as a genuinely broken service would.
		res.instances[0].State = "failed"
		s.Tick(context.Background())
	}

	assert.Len(t, res.redispatched, 6, "re-dispatch never stops for a system service")
}

func TestKeepAliveSkipsSubstrateAndAppTierServices(t *testing.T) {
	// core/agent/docker/runtime are supervised by the container runtime's restart
	// policy — the agent cannot restart itself — and a catalog TierApp service is
	// an explicit opt-out.
	res := &fakeResources{instances: []resource.SystemInstance{
		instance("system-agent", "failed", resource.HealthUnreported, 1, 2, 1, time.Hour),
		instance("system-docker", "failed", resource.HealthUnreported, 1, 2, 1, time.Hour),
		instance("system-core", "failed", resource.HealthUnreported, 1, 2, 1, time.Hour),
		instance("system-runtime", "failed", resource.HealthUnreported, 1, 2, 1, time.Hour),
		instance("system-reports", "failed", resource.HealthUnreported, 1, 2, 1, time.Hour),
	}}
	reg := &fakeRegistry{rows: []service.Registration{
		systemService("agent", "Agent"),
		systemService("docker", "Docker"),
		systemService("core", "Core"),
		systemService("runtime", "Runtime"),
		systemService("reports", "Reports"),
	}}
	tiers := fakeTiers{declared: map[string]steward.Tier{
		"agent":   steward.TierSystem, // system in the catalog, still substrate
		"runtime": steward.TierSystem,
		"reports": steward.TierApp, // explicitly not platform-critical
	}}
	em := &fakeEmitter{}
	s := sup(t, &fakeAgents{}, res, reg, em, tiers, Options{})

	s.Tick(context.Background())

	assert.Empty(t, res.redispatched)
	assert.Empty(t, em.emitted)
}

func TestKeepAliveSupervisesCatalogSilentSystemServices(t *testing.T) {
	// auth is provisioned by SetupService and never appears in the catalog.
	// Catalog silence is not an opt-out — is_system already means "must run".
	inst := instance("system-auth", "exited", resource.HealthNone, 1, 2, 2, time.Second)
	res := &fakeResources{instances: []resource.SystemInstance{inst}}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	s := sup(t, &fakeAgents{}, res, reg, &fakeEmitter{}, fakeTiers{
		declared: map[string]steward.Tier{"secret": steward.TierSystem},
	}, Options{})

	s.Tick(context.Background())

	assert.Equal(t, []uuid.UUID{inst.UUID}, res.redispatched)
}

func TestMissingInstanceIsReportedOncePerEpisodeAndNeverFabricated(t *testing.T) {
	res := &fakeResources{}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	em := &fakeEmitter{}
	s := sup(t, &fakeAgents{}, res, reg, em, nil, Options{})

	s.Tick(context.Background())
	s.Tick(context.Background())
	s.Tick(context.Background())

	require.Equal(t, 1, em.count(event.KindSystemInstanceMissing))
	ev := em.emitted[0]
	assert.Equal(t, event.SeverityCritical, ev.Severity)
	assert.Equal(t, event.SubjectService, ev.SubjectType)
	require.NotNil(t, ev.SubjectUUID)
	assert.Equal(t, reg.rows[0].UUID, *ev.SubjectUUID)
	assert.Equal(t, "system-auth", ev.Details["expected_resource_name"])
	assert.Contains(t, ev.Message, "SYSTEM_AUTH_IMAGE")
	// Core must not invent a spec: nothing is created or dispatched.
	assert.Empty(t, res.redispatched)

	// Once the instance is published the gap closes and can be reported again.
	res.instances = []resource.SystemInstance{healthyAuth()}
	s.Tick(context.Background())
	res.instances = nil
	s.Tick(context.Background())
	assert.Equal(t, 2, em.count(event.KindSystemInstanceMissing))
}

// --- (d) escalation --------------------------------------------------------

func TestEscalationFiresOncePerEpisodeAndResetsOnRecovery(t *testing.T) {
	const escalateAfter = 3
	inst := instance("system-auth", "exited", resource.HealthNone, 1, 2, 2, time.Second)
	res := &fakeResources{instances: []resource.SystemInstance{inst}}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	em := &fakeEmitter{}
	s := sup(t, &fakeAgents{}, res, reg, em, nil, Options{EscalateAfter: escalateAfter})

	// Passes 1..3 re-dispatch without escalating (strictly MORE than the budget).
	for i := 0; i < escalateAfter; i++ {
		res.instances[0].State = "exited"
		res.instances[0].ObservedGeneration = res.instances[0].Generation
		s.Tick(context.Background())
	}
	assert.Equal(t, 0, em.count(event.KindSystemRedispatchEscalated))

	// Passes 4..6 cross the threshold, but escalate exactly once.
	for i := 0; i < 3; i++ {
		res.instances[0].State = "exited"
		res.instances[0].ObservedGeneration = res.instances[0].Generation
		s.Tick(context.Background())
	}
	require.Equal(t, 1, em.count(event.KindSystemRedispatchEscalated))

	var esc event.Input
	for _, e := range em.emitted {
		if e.Kind == event.KindSystemRedispatchEscalated {
			esc = e
		}
	}
	assert.Equal(t, event.SeverityCritical, esc.Severity)
	assert.Equal(t, event.SubjectResource, esc.SubjectType)
	assert.Equal(t, escalateAfter+1, esc.Details["consecutive_redispatches"])
	assert.Contains(t, esc.Message, "never gives up")

	// Recovery closes the incident...
	res.instances[0].State = "running"
	res.instances[0].Health = resource.HealthHealthy
	res.instances[0].ObservedGeneration = res.instances[0].Generation
	s.Tick(context.Background())
	assert.Equal(t, 1, em.count(event.KindSystemRecovered))

	// ...and the counter restarts, so the next incident escalates afresh.
	before := len(res.redispatched)
	for i := 0; i < escalateAfter; i++ {
		res.instances[0].State = "exited"
		res.instances[0].Health = resource.HealthNone
		res.instances[0].ObservedGeneration = res.instances[0].Generation
		s.Tick(context.Background())
	}
	assert.Equal(t, 1, em.count(event.KindSystemRedispatchEscalated), "the episode was reset")
	assert.Equal(t, escalateAfter, len(res.redispatched)-before)
}

func TestRecoveryWithoutAnEscalationIsSilent(t *testing.T) {
	res := &fakeResources{instances: []resource.SystemInstance{healthyAuth()}}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	em := &fakeEmitter{}
	s := sup(t, &fakeAgents{}, res, reg, em, nil, Options{})

	s.Tick(context.Background())

	assert.Empty(t, em.emitted, "a healthy service must not produce events every 15s")
}

func TestAFailedRedispatchDoesNotCountTowardEscalation(t *testing.T) {
	// Otherwise a database problem would look like an unrecoverable service.
	inst := instance("system-auth", "exited", resource.HealthNone, 1, 2, 2, time.Second)
	res := &fakeResources{
		instances:     []resource.SystemInstance{inst},
		redispatchErr: errors.New("write failed"),
	}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	em := &fakeEmitter{}
	s := sup(t, &fakeAgents{}, res, reg, em, nil, Options{EscalateAfter: 1})

	for i := 0; i < 5; i++ {
		s.Tick(context.Background())
	}

	assert.Empty(t, res.redispatched)
	assert.Equal(t, 0, em.count(event.KindSystemRedispatchEscalated))
}

func TestAFailingEmitterNeverBreaksTheLoop(t *testing.T) {
	inst := instance("system-auth", "exited", resource.HealthNone, 1, 2, 2, time.Second)
	res := &fakeResources{instances: []resource.SystemInstance{inst}}
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	em := &fakeEmitter{err: errors.New("events table is gone")}
	s := sup(t, &fakeAgents{}, res, reg, em, nil, Options{EscalateAfter: 1})

	for i := 0; i < 3; i++ {
		res.instances[0].State = "exited"
		res.instances[0].ObservedGeneration = res.instances[0].Generation
		s.Tick(context.Background())
	}

	assert.Len(t, res.redispatched, 3, "recording the incident must never stop fixing it")
}

// --- bookkeeping / plumbing ------------------------------------------------

func TestEpisodesArePrunedWhenAServiceStopsBeingSupervised(t *testing.T) {
	reg := &fakeRegistry{rows: []service.Registration{systemService("auth", "Auth")}}
	s := sup(t, &fakeAgents{}, &fakeResources{}, reg, &fakeEmitter{}, nil, Options{})

	s.Tick(context.Background())
	s.mu.Lock()
	held := len(s.episodes)
	s.mu.Unlock()
	require.Equal(t, 1, held)

	reg.rows = nil
	s.Tick(context.Background())
	s.mu.Lock()
	held = len(s.episodes)
	s.mu.Unlock()
	assert.Zero(t, held, "the episode map must not grow for the life of the process")
}

func TestKeepAliveSkippedWhenTheRegistryReadFails(t *testing.T) {
	res := &fakeResources{instances: []resource.SystemInstance{
		instance("system-auth", "failed", resource.HealthUnreported, 1, 2, 1, time.Hour),
	}}
	reg := &fakeRegistry{err: errors.New("db down")}
	s := sup(t, &fakeAgents{}, res, reg, &fakeEmitter{}, nil, Options{})

	s.Tick(context.Background())

	assert.Empty(t, res.redispatched, "without the registry there is no list of what must run")
}

func TestOptionsDefaults(t *testing.T) {
	s := New(&fakeAgents{}, &fakeResources{}, &fakeRegistry{}, &fakeEmitter{}, fakeTiers{}, Options{})
	assert.Equal(t, DefaultInterval, s.opts.Interval)
	assert.Equal(t, DefaultAgentOfflineAfter, s.opts.AgentOfflineAfter)
	assert.Equal(t, DefaultStaleAfter, s.opts.StaleAfter)
	assert.Equal(t, DefaultEscalateAfter, s.opts.EscalateAfter)
	assert.Equal(t, "docker", s.opts.DeploymentMode)

	// The agent beats every 10s; the offline threshold must be several beats.
	assert.Greater(t, DefaultAgentOfflineAfter, 3*10*time.Second)
	// The stale window must exceed a pass, or supervision chases its own tail.
	assert.Greater(t, DefaultStaleAfter, DefaultInterval)
}

func TestOptionsOverridesAreHonoured(t *testing.T) {
	opts := Options{
		Interval:          time.Second,
		AgentOfflineAfter: 2 * time.Second,
		StaleAfter:        3 * time.Second,
		EscalateAfter:     9,
		DeploymentMode:    "kubernetes",
	}
	s := New(&fakeAgents{}, &fakeResources{}, &fakeRegistry{}, &fakeEmitter{}, fakeTiers{}, opts)
	assert.Equal(t, opts, s.opts)
}

func TestRunPassesImmediatelyThenStopsOnCancel(t *testing.T) {
	// The most likely moment for a system service to be down is right after Core
	// was down, so the first pass must not wait a full interval.
	agents := &fakeAgents{swept: make(chan struct{}, 1)}
	s := New(agents, &fakeResources{}, &fakeRegistry{}, &fakeEmitter{}, fakeTiers{},
		Options{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	select {
	case <-agents.swept:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("the first pass did not run immediately")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestTickIsANoOpOnACancelledContext(t *testing.T) {
	agents := &fakeAgents{}
	s := New(agents, &fakeResources{}, &fakeRegistry{}, &fakeEmitter{}, fakeTiers{}, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.Tick(ctx)

	assert.Zero(t, agents.sweeps)
}

// panickingResources reproduces a bug inside the loop. An unrecovered panic on
// the supervision goroutine would take the whole process down — the availability
// loop must not be the thing that ends availability.
type panickingResources struct{ fakeResources }

func (*panickingResources) ListSystemTier(context.Context) ([]resource.SystemInstance, error) {
	panic("boom")
}

func TestTickRecoversFromAPanic(t *testing.T) {
	s := New(&fakeAgents{}, &panickingResources{}, &fakeRegistry{}, &fakeEmitter{}, fakeTiers{}, Options{})
	assert.NotPanics(t, func() { s.Tick(context.Background()) })
}

func TestBuiltinCatalogTiersDriveSupervision(t *testing.T) {
	// The real catalog, not a stand-in: secret is system-tier and supervised,
	// agent/runtime are system-tier but substrate, and an unknown name is
	// catalog silence.
	catalog := steward.BuiltinCatalog(staticAudience{})
	s := New(&fakeAgents{}, &fakeResources{}, &fakeRegistry{}, &fakeEmitter{}, catalog, Options{})

	assert.True(t, s.supervises("secret"))
	assert.False(t, s.supervises("agent"))
	assert.False(t, s.supervises("runtime"))
	assert.False(t, s.supervises("docker"))
	assert.False(t, s.supervises("core"))
	assert.True(t, s.supervises("auth"), "catalog silence is not an opt-out")
}

type staticAudience struct{}

func (staticAudience) AudienceFor(string) string { return "https://example.test" }
