-- GA4 reports per enabled property (MH-444).
--
--   ga4_reports   the Analytics page's answer per (property, window): read
--                 from here for an hour, refreshed on demand after that and
--                 nightly by the collector (the snapshot the Overview reads).
--   ga4_daily     every day any report has returned, kept beyond Google's
--                 windows: users, sessions and views per property per day.
--
-- Numbers are exactly what GA4 answered; nothing here is estimated. A window's
-- users come from GA4's own total, never from adding up days (a returning
-- visitor is one user). Isolated by RLS (internal/db/post/81_ga4.sql).

CREATE TABLE IF NOT EXISTS ga4_reports (
    workspace_id  text        NOT NULL,
    resource_id   text        NOT NULL,
    days          integer     NOT NULL,
    -- The whole answer as the page reads it (internal/analytics.Report).
    payload       jsonb       NOT NULL DEFAULT '{}',
    -- The window's totals, for the Overview without unpacking the payload.
    users         bigint      NOT NULL DEFAULT 0,
    sessions      bigint      NOT NULL DEFAULT 0,
    views         bigint      NOT NULL DEFAULT 0,
    start_date    date,
    end_date      date,
    fetched_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id, days),
    CHECK (days IN (7, 28, 90))
);

CREATE TABLE IF NOT EXISTS ga4_daily (
    workspace_id  text        NOT NULL,
    resource_id   text        NOT NULL,
    day           date        NOT NULL,
    users         bigint      NOT NULL DEFAULT 0,
    sessions      bigint      NOT NULL DEFAULT 0,
    views         bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id, day)
);
