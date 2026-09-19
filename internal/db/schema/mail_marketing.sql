-- Email marketing (ported from fadymondy.com-v2 subscribers / campaigns / smtp_services),
-- made multi-tenant: every row carries workspace_id and is isolated by RLS
-- (internal/db/post/20_rls.sql). Hand-written Postgres, like the other fragments.
--
-- fadymondy.com has one flat subscriber table per site. Here a contact is a person
-- in a workspace, and consent is per list: the same address may be confirmed on one
-- list and unsubscribed from another.

CREATE TABLE IF NOT EXISTS mail_lists (
    id            text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    name          text        NOT NULL,
    description   text        NOT NULL DEFAULT '',
    double_opt_in boolean     NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS contacts (
    id           text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    email        text        NOT NULL,
    name         text        NOT NULL DEFAULT '',
    locale       text        NOT NULL DEFAULT 'en',
    source       text        NOT NULL DEFAULT 'manual',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, email),
    CHECK (locale IN ('en', 'ar'))
);

-- One contact on one list. The token is the double opt-in AND the unsubscribe
-- credential; random, never derived from the address (a hash of the email would
-- let anyone confirm an address they do not own).
CREATE TABLE IF NOT EXISTS list_subscriptions (
    id              text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    list_id         text        NOT NULL,
    contact_id      text        NOT NULL,
    status          text        NOT NULL DEFAULT 'pending',
    token           text        NOT NULL UNIQUE,
    confirmed_at    timestamptz,
    unsubscribed_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (list_id, contact_id),
    CHECK (status IN ('pending', 'subscribed', 'unsubscribed', 'bounced'))
);

CREATE TABLE IF NOT EXISTS campaigns (
    id              text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    list_id         text        NOT NULL,
    subject         text        NOT NULL,
    body            text        NOT NULL,
    locale          text        NOT NULL DEFAULT 'en',
    status          text        NOT NULL DEFAULT 'draft',
    recipient_count bigint      NOT NULL DEFAULT 0,
    sent_count      bigint      NOT NULL DEFAULT 0,
    failed_count    bigint      NOT NULL DEFAULT 0,
    last_error      text        NOT NULL DEFAULT '',
    created_by      text        NOT NULL,
    sent_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (status IN ('draft', 'sending', 'sent', 'failed')),
    CHECK (locale IN ('en', 'ar'))
);

-- One SMTP service per workspace. The password is AES-GCM sealed with a key
-- derived from AUTH_SECRET (internal/marketing/secret.go) and never returned.
CREATE TABLE IF NOT EXISTS smtp_settings (
    workspace_id    text PRIMARY KEY,
    host            text        NOT NULL,
    port            bigint      NOT NULL DEFAULT 587,
    username        text        NOT NULL DEFAULT '',
    password_sealed text        NOT NULL DEFAULT '',
    from_name       text        NOT NULL DEFAULT '',
    from_email      text        NOT NULL,
    tls_mode        text        NOT NULL DEFAULT 'starttls',
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (tls_mode IN ('none', 'starttls', 'tls'))
);
