-- Leads (MH-390): what a visitor sends through an inquiry form on a customer's
-- website, before anybody has decided it is a contact, a company or a deal.
--
-- A lead is deliberately NOT a contact. A contact is somebody the workspace
-- keeps; a lead is a message from a stranger that may be spam, may be a
-- duplicate of a customer who already exists, and may never become anything.
-- Converting one (internal/leads/convert.go) is the moment it becomes CRM data:
-- the contact is deduped on email, the company comes from the email domain
-- (never a free-mail one), a deal opens on the pipeline, and the message is the
-- first thing on the timeline. The ids it converted into are kept here, so the
-- lead links to what it became.
--
-- Every row carries workspace_id and is isolated by RLS; the foreign keys and
-- the policy are in internal/db/post/32_leads.sql, in one file so a renumber is
-- a rename.
CREATE TABLE IF NOT EXISTS leads (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    -- The inquiry form it came through (a subscribe_forms row of kind 'inquiry').
    -- Nullable, SET NULL on delete: deleting a form must not delete the leads it
    -- collected. form_name is the snapshot that keeps a label on them.
    form_id       text,
    form_name     text        NOT NULL DEFAULT '',
    status        text        NOT NULL DEFAULT 'new',

    -- What the visitor typed. Only email and message are required at intake.
    name          text        NOT NULL DEFAULT '',
    email         text        NOT NULL,
    phone         text        NOT NULL DEFAULT '',
    company       text        NOT NULL DEFAULT '',
    message       text        NOT NULL,
    locale        text        NOT NULL DEFAULT 'en',

    -- First-touch attribution, captured by the embed snippet in the visitor's
    -- browser (the landing page's query string and document.referrer). Stored
    -- as sent, bounded by the endpoint's maxLength; the snippet is the only
    -- thing that fills these, so they are marketing data, not proof.
    utm_source    text        NOT NULL DEFAULT '',
    utm_medium    text        NOT NULL DEFAULT '',
    utm_campaign  text        NOT NULL DEFAULT '',
    utm_term      text        NOT NULL DEFAULT '',
    utm_content   text        NOT NULL DEFAULT '',
    referrer      text        NOT NULL DEFAULT '',
    gclid         text        NOT NULL DEFAULT '',
    landing_page  text        NOT NULL DEFAULT '',
    -- The browser Origin the form was posted from — already checked against the
    -- form's allow-list, so this is which of the allowed sites it was.
    origin        text        NOT NULL DEFAULT '',

    -- What it converted into. Composite (workspace_id, x_id) foreign keys, ON
    -- DELETE SET NULL: deleting the deal later must not delete the lead.
    contact_id    text,
    company_id    text,
    deal_id       text,
    converted_at  timestamptz,
    converted_by  text,

    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (status IN ('new', 'contacted', 'qualified', 'converted', 'spam')),
    CHECK (locale IN ('en', 'ar'))
);
