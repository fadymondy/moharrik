-- MH-679: "we must have manage workspaces page that we can edit/delete/add
-- team avatar and add team members to the team" (Fady, verbatim).
--
-- One uploaded picture per workspace, stored the way contact_avatars stores a
-- contact's (MH-602): small bytes in Postgres, capped at 2MB by
-- internal/workspaces/manage.go. The workspace id IS the key, so a re-upload
-- replaces it, and deleting the workspace takes the picture with it.
--
-- Deliberately not in migrate.TenantTables: only the owner pool touches it,
-- with explicit workspace predicates, because it is read from account-level
-- routes (/api/me/workspaces/{id}/avatar) that carry no X-Workspace-ID — the
-- workspaces list shows every workspace's picture at once.
CREATE TABLE IF NOT EXISTS workspace_avatars (
    workspace_id text        PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    content_type text        NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/webp')),
    size_bytes   bigint      NOT NULL DEFAULT 0,
    data         bytea       NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
