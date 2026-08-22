package event

import (
	"context"

	"github.com/maintainerd/core/internal/storage"
)

// Repository is the platform-event bounded context's data contract. It touches
// exactly one table: events are append-only by design — there is no update and
// no delete, because an escalation log an operator can edit is not evidence.
// *storage.Queries satisfies it; tests pass a mock.
type Repository interface {
	CreatePlatformEvent(ctx context.Context, arg storage.CreatePlatformEventParams) (storage.PlatformEvent, error)
	ListPlatformEvents(ctx context.Context, arg storage.ListPlatformEventsParams) ([]storage.PlatformEvent, error)
	CountPlatformEvents(ctx context.Context) (int64, error)
	ListPlatformEventsBySubject(ctx context.Context, arg storage.ListPlatformEventsBySubjectParams) ([]storage.PlatformEvent, error)
}
