-- Canned replies (MH-383): the answers a team types again and again, kept once
-- per workspace, in English and Arabic.
--
-- Bilingual because the inbox is: the variant inserted into a reply follows the
-- CONVERSATION's language, not the member's UI language — someone reading the
-- inbox in English still answers an Arabic customer in Arabic. Either variant
-- may be empty, not both; the empty one falls back to the other.
--
-- Every row carries workspace_id and is isolated by RLS; the foreign keys and
-- the policy are in internal/db/post/66_canned_replies.sql, in one file so a
-- renumber is a rename.
CREATE TABLE IF NOT EXISTS canned_replies (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    -- What the picker shows. A label for the team, never sent.
    title        text        NOT NULL,
    body_en      text        NOT NULL DEFAULT '',
    body_ar      text        NOT NULL DEFAULT '',
    position     bigint      NOT NULL DEFAULT 0,
    created_by   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (body_en <> '' OR body_ar <> '')
);
