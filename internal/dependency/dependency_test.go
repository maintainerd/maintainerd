package dependency

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fastOptions() Options {
	return Options{
		Interval:       5 * time.Millisecond,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		PassTimeout:    time.Second,
	}
}

// Before the first pass a dependency is NOT ready. "We have not looked yet" and
// "it answered" must not be the same state, or core would report itself ready for
// the window between the listener opening and the first reading.
func TestUnprobedIsNotReady(t *testing.T) {
	rec := New([]Probe{{Name: "secret", Check: func(context.Context) error { return nil }}}, fastOptions())

	assert.False(t, rec.Settled())
	require.ErrorIs(t, rec.State("secret"), ErrNotProbed)
	require.ErrorIs(t, rec.State("nonexistent"), ErrNotProbed)
}

// A configured-but-unreachable dependency does NOT stop the loop. This is the
// property the whole package exists for: auth, core and secret start together, so
// core converges toward reachability instead of dying on it.
func TestUnreachableDependencyKeepsConverging(t *testing.T) {
	var passes atomic.Int64
	rec := New([]Probe{{
		Name: "secret",
		Check: func(context.Context) error {
			passes.Add(1)
			return errors.New("dial tcp 10.0.0.5:8092: connect: connection refused")
		},
	}}, fastOptions())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rec.Run(ctx); close(done) }()

	require.Eventually(t, func() bool { return passes.Load() >= 2 }, time.Second, time.Millisecond,
		"the loop must keep retrying rather than give up after one failure")
	assert.False(t, rec.Settled())
	assert.Error(t, rec.State("secret"))

	select {
	case <-done:
		t.Fatal("Run returned while a dependency was unreachable — it must converge, not exit")
	default:
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return on context cancellation")
	}
}

// The whole lifecycle in one test: unreachable at boot, settles when the
// dependency arrives, and DEGRADES again when it drops afterwards — without the
// loop ever returning. A vault blip must not become a control-plane outage.
func TestDependencyDroppingAfterSettlingDegradesWithoutExiting(t *testing.T) {
	var up atomic.Bool
	rec := New([]Probe{{
		Name: "secret",
		Check: func(context.Context) error {
			if up.Load() {
				return nil
			}
			return errors.New("connection refused")
		},
	}}, fastOptions())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { rec.Run(ctx); close(done) }()

	require.Eventually(t, func() bool { return rec.State("secret") != nil }, time.Second, time.Millisecond)

	up.Store(true)
	require.Eventually(t, rec.Settled, time.Second, time.Millisecond,
		"the loop must notice a dependency that arrives late")

	up.Store(false)
	require.Eventually(t, func() bool { return !rec.Settled() }, time.Second, time.Millisecond,
		"a dependency that drops after settling must degrade readiness")

	select {
	case <-done:
		t.Fatal("Run returned when a settled dependency dropped — the process must keep serving")
	default:
	}
}

// One unhealthy dependency must not hide the others' state.
func TestPassRecordsEveryDependencyIndependently(t *testing.T) {
	rec := New([]Probe{
		{Name: "auth", Check: func(context.Context) error { return nil }},
		{Name: "secret", Check: func(context.Context) error { return errors.New("connection refused") }},
	}, fastOptions())

	rec.Pass(context.Background())

	assert.NoError(t, rec.State("auth"))
	assert.Error(t, rec.State("secret"))
	assert.False(t, rec.Settled())
	assert.Equal(t, []string{"auth", "secret"}, rec.Names())
}

// A probe that ignores its context must not be waited on forever — the pass
// deadline is what keeps the loop (and therefore readiness) moving.
func TestPassIsBounded(t *testing.T) {
	opts := fastOptions()
	opts.PassTimeout = 10 * time.Millisecond
	rec := New([]Probe{{
		Name: "secret",
		Check: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}}, opts)

	start := time.Now()
	rec.Pass(context.Background())
	assert.Less(t, time.Since(start), time.Second)
	assert.Error(t, rec.State("secret"))
}
