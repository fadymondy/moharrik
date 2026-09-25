-- MH-514: prospect discovery's "who" (industries, sizes, regions, keywords)
-- moves onto the workspace's CRM ICP (icp_profiles, MH-395) instead of its
-- own copy in prospect_profiles, so a workspace describes its ideal customer
-- once. Idempotent, like every post/ migration.
--
-- Numbered 514 (the card's own number, not the next sequential slot) because
-- several branches add post/ migrations concurrently off the same base; a
-- ticket number is one no other card will also pick, so two branches merging
-- into integrate never collide on the same filename or prefix the way two
-- branches both reaching for "173_..." could. See internal/db/migrate/order_test.go
-- for why the numeric prefix (not the name) decides apply order — any number
-- higher than what came before is enough; it does not need to be sequential.

-- Backfill: every workspace that already has a prospect_profiles row gets an
-- icp_profiles row before the link below is enforced — reusing one if it
-- already exists (an ICP set up first wins; this never overwrites it), or
-- building one from the prospect profile's own industries/keywords/regions
-- so discovery keeps finding what it already found. Default weights match
-- internal/icp.Default() for the criteria new here; sizes has nothing to
-- seed from (prospect_profiles never carried a size field), so it starts
-- empty like a workspace that configured its ICP by hand would.
INSERT INTO icp_profiles (workspace_id, criteria)
SELECT pp.workspace_id,
    jsonb_build_object(
        'industries', jsonb_build_object('values', to_jsonb(pp.industries), 'weight', 3),
        'sizes',      jsonb_build_object('values', '[]'::jsonb, 'weight', 2),
        'regions',    jsonb_build_object('values', to_jsonb(pp.regions), 'weight', 2),
        'keywords',   jsonb_build_object('values', to_jsonb(pp.keywords), 'weight', 2),
        'signals', jsonb_build_object(
            'open_deal', 3, 'won_deal', 3, 'lost_deal', 0, 'inbound_lead', 2,
            'conversation', 1, 'subscribed', 1, 'whatsapp_opt_in', 1, 'recent_activity', 2
        )
    )
FROM prospect_profiles pp
WHERE NOT EXISTS (SELECT 1 FROM icp_profiles ip WHERE ip.workspace_id = pp.workspace_id);

-- No FK from prospect_profiles to icp_profiles: the link is logical (same
-- workspace_id), not enforced in the schema. icp_profiles existing is not
-- the same as icp_profiles being configured — internal/icp.LoadProfile
-- treats "no row" as icp.Default() and internal/icp.ProfileOut.Configured
-- means "a person saved this ICP" (the CRM ICP settings page's own
-- invariant). A hard FK would force prospect discovery to silently create an
-- empty icp_profiles row on its own first read (internal/prospects.ensureProfile),
-- which would flip that flag to true behind the ICP settings page's back —
-- exactly the two-pages-disagree bug MH-514 exists to remove. The backfill
-- above still runs, so existing workspaces keep finding what they already
-- found; new workspaces just read icp.Default() until someone saves an ICP,
-- on either page.
