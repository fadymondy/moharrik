-- The account cycle (internal/account). Tables are in internal/db/schema/auth_cycle.sql.

-- Whether the account's address is proven: a mailed link was opened, or a password reset reached
-- it. Accounts that existed before verification did are grandfathered in as verified — once, when
-- the column is first added, so a later re-run of this file never verifies anyone.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'platform_users' AND column_name = 'email_verified_at') THEN
        ALTER TABLE platform_users ADD COLUMN email_verified_at timestamptz;
        INSERT INTO platform_users (user_id, email_verified_at)
            SELECT id, now() FROM users
            ON CONFLICT (user_id) DO UPDATE SET email_verified_at = now();
    END IF;
END $$;

-- Links and refresh tokens go with their account.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'auth_email_tokens_user_fk') THEN
        ALTER TABLE auth_email_tokens ADD CONSTRAINT auth_email_tokens_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'app_refresh_tokens_user_fk') THEN
        ALTER TABLE app_refresh_tokens ADD CONSTRAINT app_refresh_tokens_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
    END IF;
END $$;

-- Owner pool only, the same as platform_users.
ALTER TABLE auth_email_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_email_tokens FORCE ROW LEVEL SECURITY;
ALTER TABLE app_refresh_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_refresh_tokens FORCE ROW LEVEL SECURITY;
