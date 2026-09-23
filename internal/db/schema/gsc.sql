-- Search Console performance per enabled site (MH-445), imported daily.
--
--   gsc_search_analytics_daily  date × query × page × country × device, as
--       Search Analytics returns it. Google leaves anonymised queries out of
--       query-level rows, so these add up to LESS than the site's totals.
--   gsc_search_totals_daily     date × country × device, WITHOUT the query and
--       page dimensions: the site's real totals, anonymised queries included.
--       Totals and the trend come from here unless a query or page filter is
--       set, and the page says which.
--   gsc_import_days             which days have been imported and when, so a
--       day with no data is known to be empty rather than missing, and days
--       young enough to still change (Google finalises over ~3 days) are
--       imported again.
--
-- position is the impression-weighted average Google reports; any sum over
-- rows re-weights it by impressions. Isolated by RLS (82_gsc.sql).

CREATE TABLE IF NOT EXISTS gsc_search_analytics_daily (
    workspace_id  text             NOT NULL,
    resource_id   text             NOT NULL,
    date          date             NOT NULL,
    query         text             NOT NULL DEFAULT '',
    page          text             NOT NULL DEFAULT '',
    country       text             NOT NULL DEFAULT '',
    device        text             NOT NULL DEFAULT '',
    clicks        bigint           NOT NULL DEFAULT 0,
    impressions   bigint           NOT NULL DEFAULT 0,
    ctr           double precision NOT NULL DEFAULT 0,
    position      double precision NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS gsc_search_totals_daily (
    workspace_id  text             NOT NULL,
    resource_id   text             NOT NULL,
    date          date             NOT NULL,
    country       text             NOT NULL DEFAULT '',
    device        text             NOT NULL DEFAULT '',
    clicks        bigint           NOT NULL DEFAULT 0,
    impressions   bigint           NOT NULL DEFAULT 0,
    ctr           double precision NOT NULL DEFAULT 0,
    position      double precision NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, resource_id, date, country, device)
);

CREATE TABLE IF NOT EXISTS gsc_import_days (
    workspace_id  text        NOT NULL,
    resource_id   text        NOT NULL,
    date          date        NOT NULL,
    detail_rows   integer     NOT NULL DEFAULT 0,
    -- The day had more query-level rows than one import keeps.
    truncated     boolean     NOT NULL DEFAULT false,
    imported_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id, date)
);
