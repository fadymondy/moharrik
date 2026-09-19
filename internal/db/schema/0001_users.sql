-- The togo auth plugin's own users table, byte-for-byte the DDL it runs at boot
-- (auth@v0.8.0 ensureSchema). Declared here too so `togo migrate` can add the
-- foreign keys in internal/db/post/ before the API has ever booted, and so sqlc
-- knows the table exists.
CREATE TABLE IF NOT EXISTS users (
    id text PRIMARY KEY,
    email text UNIQUE NOT NULL,
    password_hash text NOT NULL,
    roles text NOT NULL DEFAULT '',
    permissions text NOT NULL DEFAULT '',
    created_at text NOT NULL
);
