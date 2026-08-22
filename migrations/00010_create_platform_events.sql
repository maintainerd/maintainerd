-- +goose Up
-- The platform's durable escalation log. Supervision cannot be a log line: a
-- system service that will not come back has to leave a record an operator (or a
-- console, or an alerting pipeline) can read AFTER the fact, on a process that
-- has since restarted. slog output is per-process and ephemeral; this table is
-- the platform's memory of "we tried, it did not recover, a human is needed".
--
-- Deliberately generic rather than a supervisor-specific table: kind + severity +
-- subject + details JSONB covers every future platform-level signal (quota,
-- certificate expiry, drift) without another migration.
--
--   tenant_id     NULL for platform-scoped events. Supervision of system-tier
--                 workloads is a PLATFORM concern, not a tenant's — the tenant
--                 that happens to own the row is incidental — so the supervisor
--                 writes NULL and locates the row via subject_uuid instead.
--   kind          stable machine-readable event type (see internal/event).
--   severity      info | warning | critical.
--   subject_type  agent | resource | service — what subject_uuid points at.
--   details       structured context; never a free-form second message field.
CREATE TABLE IF NOT EXISTS platform_events (
    event_id     BIGSERIAL PRIMARY KEY,
    event_uuid   UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    tenant_id    BIGINT REFERENCES tenants (tenant_id) ON DELETE CASCADE,
    kind         VARCHAR(50) NOT NULL,
    severity     VARCHAR(20) NOT NULL DEFAULT 'warning',
    subject_type VARCHAR(50) NOT NULL DEFAULT '',
    subject_uuid UUID,
    message      TEXT NOT NULL,
    details      JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The console's default read: newest first, unfiltered.
CREATE INDEX IF NOT EXISTS idx_platform_events_created_at ON platform_events (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_platform_events_kind ON platform_events (kind);
CREATE INDEX IF NOT EXISTS idx_platform_events_severity ON platform_events (severity);
CREATE INDEX IF NOT EXISTS idx_platform_events_tenant_id ON platform_events (tenant_id);
-- "what has happened to THIS agent/resource" — the incident-timeline read.
CREATE INDEX IF NOT EXISTS idx_platform_events_subject ON platform_events (subject_type, subject_uuid);
CREATE INDEX IF NOT EXISTS idx_platform_events_details ON platform_events USING GIN (details);

-- +goose Down
DROP TABLE IF EXISTS platform_events;
