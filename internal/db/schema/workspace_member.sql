-- Hand-written Postgres DDL for WorkspaceMember (see workspace.sql for why).
-- role: owner | admin | manager | member | client  (internal/authz/can.go)
-- status: active | suspended
CREATE TABLE IF NOT EXISTS workspace_members (
    id              text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    user_id         text        NOT NULL,
    role            text        NOT NULL,
    cost_rate_minor bigint,
    bill_rate_minor bigint,
    status          text        NOT NULL DEFAULT 'active',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id),
    CHECK (role IN ('owner', 'admin', 'manager', 'member', 'client')),
    CHECK (status IN ('active', 'suspended'))
);
