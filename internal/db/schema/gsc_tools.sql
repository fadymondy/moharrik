-- Search Console URL inspection (MH-446).
--
--   gsc_url_inspections       the latest inspection of each URL per enabled site:
--                             the verdict and states Google reported, and its
--                             whole answer for the detail view.
--   gsc_inspection_quota      the inspection ledger. Google allows 2,000
--                             inspections a day per property (and 600 a
--                             minute) for the whole OAuth project, so the cap
--                             is per SITE, summed across every workspace that
--                             reads it. Owner pool only, like youtube_quota.
--
-- Sitemaps are read live from Google and not stored.

CREATE TABLE IF NOT EXISTS gsc_url_inspections (
    workspace_id     text        NOT NULL,
    resource_id      text        NOT NULL,
    url              text        NOT NULL,
    verdict          text        NOT NULL DEFAULT '',
    coverage_state   text        NOT NULL DEFAULT '',
    indexing_state   text        NOT NULL DEFAULT '',
    robots_txt_state text        NOT NULL DEFAULT '',
    page_fetch_state text        NOT NULL DEFAULT '',
    last_crawl_time  timestamptz,
    google_canonical text        NOT NULL DEFAULT '',
    user_canonical   text        NOT NULL DEFAULT '',
    mobile_verdict   text        NOT NULL DEFAULT '',
    rich_verdict     text        NOT NULL DEFAULT '',
    result_link      text        NOT NULL DEFAULT '',
    result           jsonb       NOT NULL DEFAULT '{}',
    inspected_by     text        NOT NULL DEFAULT '',
    inspected_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id, url)
);

CREATE TABLE IF NOT EXISTS gsc_inspection_quota (
    -- The Pacific day, as Google counts it.
    day           date        NOT NULL,
    site_url      text        NOT NULL,
    workspace_id  text        NOT NULL,
    used          integer     NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (day, site_url, workspace_id)
);
