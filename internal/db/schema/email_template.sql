-- Email templates (MH-661): the reply-email counterpart to canned_replies —
-- a name, a subject line and a Markdown body, in English and Arabic, that a
-- member picks from the email composer instead of writing the same answer
-- from scratch.
--
-- Bilingual the same way canned_replies is: one row with _en/_ar columns, not
-- a separate row per locale — the variant used follows the CONVERSATION's
-- language, not the member's own UI language. Either variant may be empty,
-- not both.
--
-- Subject and body both support placeholders (contact.first_name,
-- contact.name, contact.email, workspace.name, agent.name), resolved by a
-- small dedicated renderer (internal/inbox/email_template.go) that leaves any
-- OTHER {{...}} untouched rather than blanking it — do not reuse
-- internal/rules.Render, which blanks unknown placeholders.
--
-- Every row carries workspace_id and is isolated by RLS; the foreign keys and
-- the policy are in internal/db/post/181_email_templates.sql, in one file so
-- a renumber is a rename.
CREATE TABLE IF NOT EXISTS email_templates (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    -- What the picker shows. A label for the team, never sent.
    name         text        NOT NULL,
    subject_en   text        NOT NULL DEFAULT '',
    subject_ar   text        NOT NULL DEFAULT '',
    body_en      text        NOT NULL DEFAULT '',
    body_ar      text        NOT NULL DEFAULT '',
    position     bigint      NOT NULL DEFAULT 0,
    created_by   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (body_en <> '' OR body_ar <> '')
);
