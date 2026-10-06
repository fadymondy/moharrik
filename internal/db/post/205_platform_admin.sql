-- The superadmin panel (MH-1184). Tables are in internal/db/schema/platform.sql.

-- A suspended workspace answers 403 to its members (tenant.Resolve); the admin panel can still
-- open it. Nullable, so existing rows stay active.
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS suspended_at timestamptz;
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS suspended_reason text;

-- Account flags go with the account.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_users_user_fk') THEN
        ALTER TABLE platform_users ADD CONSTRAINT platform_users_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
    END IF;
END $$;

-- Owner pool only: RLS on with no policy, the same as admin_mcp_tokens. If a grant to the app
-- role is ever added by mistake, it still reads nothing.
ALTER TABLE platform_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_users FORCE ROW LEVEL SECURITY;
ALTER TABLE admin_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_audit FORCE ROW LEVEL SECURITY;
