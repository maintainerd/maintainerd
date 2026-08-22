package event

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/storage"
)

// fakeRepo implements Repository over an in-memory append-only slice, matching
// the table's shape (there is no update and no delete by design).
type fakeRepo struct {
	rows    []storage.PlatformEvent
	written []storage.CreatePlatformEventParams
}

func (f *fakeRepo) CreatePlatformEvent(_ context.Context, arg storage.CreatePlatformEventParams) (storage.PlatformEvent, error) {
	f.written = append(f.written, arg)
	row := storage.PlatformEvent{
		EventID:     int64(len(f.rows) + 1),
		EventUuid:   uuid.New(),
		TenantID:    arg.TenantID,
		Kind:        arg.Kind,
		Severity:    arg.Severity,
		SubjectType: arg.SubjectType,
		SubjectUuid: arg.SubjectUuid,
		Message:     arg.Message,
		Details:     arg.Details,
	}
	f.rows = append(f.rows, row)
	return row, nil
}

func (f *fakeRepo) ListPlatformEvents(_ context.Context, arg storage.ListPlatformEventsParams) ([]storage.PlatformEvent, error) {
	// Newest first, like the SQL's ORDER BY created_at DESC, event_id DESC.
	out := []storage.PlatformEvent{}
	for i := len(f.rows) - 1; i >= 0; i-- {
		out = append(out, f.rows[i])
	}
	if int(arg.Offset) >= len(out) {
		return []storage.PlatformEvent{}, nil
	}
	out = out[arg.Offset:]
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakeRepo) CountPlatformEvents(context.Context) (int64, error) {
	return int64(len(f.rows)), nil
}

func (f *fakeRepo) ListPlatformEventsBySubject(_ context.Context, arg storage.ListPlatformEventsBySubjectParams) ([]storage.PlatformEvent, error) {
	out := []storage.PlatformEvent{}
	for i := len(f.rows) - 1; i >= 0; i-- {
		r := f.rows[i]
		if r.SubjectType == arg.SubjectType && r.SubjectUuid == arg.SubjectUuid {
			out = append(out, r)
		}
	}
	return out, nil
}

func TestEmitValidation(t *testing.T) {
	tests := []struct {
		name    string
		in      Input
		wantErr bool
	}{
		{"kind and message present", Input{Kind: KindAgentOffline, Message: "gone"}, false},
		{"missing kind", Input{Message: "gone"}, true},
		{"missing message", Input{Kind: KindAgentOffline}, true},
		{"blank kind", Input{Kind: "   ", Message: "gone"}, true},
		{"blank message", Input{Kind: KindAgentOffline, Message: "  "}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			_, err := NewService(repo).Emit(context.Background(), tt.in)
			if !tt.wantErr {
				require.NoError(t, err)
				require.Len(t, repo.written, 1)
				return
			}
			var verr *apperror.ValidationError
			assert.ErrorAs(t, err, &verr)
			assert.Empty(t, repo.written, "an unidentifiable event must never be written")
		})
	}
}

func TestEmitDefaultsAndSubject(t *testing.T) {
	subject := uuid.New()
	repo := &fakeRepo{}
	ev, err := NewService(repo).Emit(context.Background(), Input{
		Kind:        KindSystemRedispatchEscalated,
		SubjectType: SubjectResource,
		SubjectUUID: &subject,
		Message:     "auth is not recovering",
		Details:     map[string]any{"consecutive_redispatches": 6},
	})
	require.NoError(t, err)
	require.Len(t, repo.written, 1)

	w := repo.written[0]
	assert.Equal(t, KindSystemRedispatchEscalated, w.Kind)
	assert.Equal(t, SeverityWarning, w.Severity, "an unset severity defaults to warning, never to empty")
	assert.Equal(t, SubjectResource, w.SubjectType)
	require.True(t, w.SubjectUuid.Valid)
	assert.Equal(t, subject, uuid.UUID(w.SubjectUuid.Bytes))
	assert.False(t, w.TenantID.Valid, "supervision events are platform-scoped")
	assert.JSONEq(t, `{"consecutive_redispatches":6}`, string(w.Details))

	require.NotNil(t, ev.SubjectUUID)
	assert.Equal(t, subject, *ev.SubjectUUID)
	assert.Equal(t, float64(6), ev.Details["consecutive_redispatches"])
}

func TestEmitDetailsDefaultToEmptyObject(t *testing.T) {
	// details is NOT NULL DEFAULT '{}' — a nil map must not become SQL NULL.
	repo := &fakeRepo{}
	_, err := NewService(repo).Emit(context.Background(), Input{Kind: "x.y", Message: "m"})
	require.NoError(t, err)
	assert.Equal(t, "{}", string(repo.written[0].Details))
}

func TestEmitTruncatesOverlongColumns(t *testing.T) {
	// Losing the record of an incident because its kind was two characters too
	// long would be the worst possible trade — truncate, never reject.
	long := ""
	for i := 0; i < 80; i++ {
		long += "k"
	}
	repo := &fakeRepo{}
	_, err := NewService(repo).Emit(context.Background(), Input{
		Kind: long, Severity: long, SubjectType: long, Message: "m",
	})
	require.NoError(t, err)
	assert.Len(t, repo.written[0].Kind, maxKindLen)
	assert.Len(t, repo.written[0].Severity, maxSeverityLen)
	assert.Len(t, repo.written[0].SubjectType, maxSubjectTypeLen)
}

func TestListNewestFirstWithTotal(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	for _, m := range []string{"first", "second", "third"} {
		_, err := svc.Emit(context.Background(), Input{Kind: "x.y", Message: m})
		require.NoError(t, err)
	}

	items, total, err := svc.List(context.Background(), 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, items, 2)
	assert.Equal(t, "third", items[0].Message)
	assert.Equal(t, "second", items[1].Message)

	items, _, err = svc.List(context.Background(), 2, 2)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "first", items[0].Message)
}

func TestListNormalizesPaging(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	_, err := svc.Emit(context.Background(), Input{Kind: "x.y", Message: "m"})
	require.NoError(t, err)

	// A negative page and an absurd limit must not reach the query as-is.
	items, total, err := svc.List(context.Background(), -3, 5000)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, items, 1)
}

func TestListBySubject(t *testing.T) {
	res := uuid.New()
	other := uuid.New()
	repo := &fakeRepo{}
	svc := NewService(repo)
	for _, s := range []uuid.UUID{res, other, res} {
		id := s
		_, err := svc.Emit(context.Background(), Input{
			Kind: KindSystemHostUnreachable, Message: "m",
			SubjectType: SubjectResource, SubjectUUID: &id,
		})
		require.NoError(t, err)
	}

	got, err := svc.ListBySubject(context.Background(), SubjectResource, res, 1, 20)
	require.NoError(t, err)
	assert.Len(t, got, 2)

	_, err = svc.ListBySubject(context.Background(), "", res, 1, 20)
	var verr *apperror.ValidationError
	assert.ErrorAs(t, err, &verr)
}
