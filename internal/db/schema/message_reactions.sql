-- MH-664: emoji reactions on inbox messages. One row per reactor per message:
-- reacting again with the same emoji removes it, a different one replaces it.
--
-- reactor_key says WHO reacted, in the terms the channel itself uses:
--   'customer'   — the person on the other end (WhatsApp reactionMessage)
--   'team'       — the business on a WhatsApp thread. WhatsApp shows the
--                  number's reaction, not a member's, so every member (and a
--                  reaction made on the phone itself) shares this one slot;
--                  user_id records which member it was, NULL from the phone.
--   'user:<id>'  — a member on an in-app-only channel (email, widget, …),
--                  where each member's reaction is their own.
-- Constraints, indexes and RLS live in internal/db/post/196_message_reactions.sql.
CREATE TABLE IF NOT EXISTS message_reactions (
    workspace_id text        NOT NULL,
    message_id   text        NOT NULL,
    reactor_key  text        NOT NULL,
    user_id      text,
    emoji        text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, message_id, reactor_key)
);
