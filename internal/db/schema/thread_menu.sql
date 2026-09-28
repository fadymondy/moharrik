-- MH-662: the thread ⋮ menu's own storage — starred messages and "delete for
-- us" hides, both per member like conversation_prefs (180_conversation_organise.sql):
-- a member's star or hide is their own view of the thread, not the team's.
-- "Delete for us" never touches WhatsApp itself, only Moharrik's own copy.
-- Constraints, indexes and RLS live in internal/db/post/193_thread_menu.sql.
CREATE TABLE IF NOT EXISTS message_stars (
    workspace_id text        NOT NULL,
    message_id   text        NOT NULL,
    user_id      text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, message_id, user_id)
);

CREATE TABLE IF NOT EXISTS message_hides (
    workspace_id text        NOT NULL,
    message_id   text        NOT NULL,
    user_id      text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, message_id, user_id)
);
