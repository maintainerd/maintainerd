-- name: CreatePlatformEvent :one
INSERT INTO platform_events (tenant_id, kind, severity, subject_type, subject_uuid, message, details)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListPlatformEvents :many
-- Newest first: an operator reading the escalation log wants the current
-- incident, not the install's first boot.
SELECT * FROM platform_events
ORDER BY created_at DESC, event_id DESC
LIMIT $1 OFFSET $2;

-- name: CountPlatformEvents :one
SELECT count(*) FROM platform_events;

-- name: ListPlatformEventsBySubject :many
-- The incident timeline for one agent/resource/service.
SELECT * FROM platform_events
WHERE subject_type = $1 AND subject_uuid = $2
ORDER BY created_at DESC, event_id DESC
LIMIT $3 OFFSET $4;
