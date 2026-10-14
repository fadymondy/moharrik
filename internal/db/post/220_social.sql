-- Sign in with Google and Apple (internal/social). Tables are in internal/db/schema/social.sql.

-- A provider account goes with the Moharrik account it signs in as.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'user_identities_user_fk') THEN
        ALTER TABLE user_identities ADD CONSTRAINT user_identities_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
    END IF;
END $$;

-- Owner pool only: RLS on with no policy, like platform_users. A grant to the app role added by
-- mistake still reads nothing.
ALTER TABLE user_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_identities FORCE ROW LEVEL SECURITY;
ALTER TABLE social_login_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_login_states FORCE ROW LEVEL SECURITY;
ALTER TABLE social_used_nonces ENABLE ROW LEVEL SECURITY;
ALTER TABLE social_used_nonces FORCE ROW LEVEL SECURITY;
