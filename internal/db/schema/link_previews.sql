-- MH-656: a URL's preview card (og:title, og:description, og:image) as the
-- inbox thread shows it under a message. Cached per workspace so a thread
-- that mentions the same link fifty times fetches it once, and so a link a
-- member opens is fetched by this server (with its private-network guard in
-- internal/inbox/linkpreview.go), never by pasting it into a browser. RLS and
-- the index live in internal/db/post/179_message_status_link_previews.sql.
CREATE TABLE IF NOT EXISTS link_previews (
    workspace_id text        NOT NULL,
    url          text        NOT NULL,
    status       text        NOT NULL DEFAULT 'ok',
    title        text        NOT NULL DEFAULT '',
    description  text        NOT NULL DEFAULT '',
    image_url    text        NOT NULL DEFAULT '',
    site_name    text        NOT NULL DEFAULT '',
    fetched_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, url)
);
