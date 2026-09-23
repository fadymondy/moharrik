package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/reports"
)

/*
ReportTools: the CRM report (MH-394), answered by reports.CRMFor — the function
GET /api/w/reports/crm calls — so the figures an agent reads are the figures on
the Reports page, to the unit.
*/
func ReportTools() []Tool {
	return []Tool{{
		Name: "crm_report",
		Description: "The CRM report for a range of days (workspace time zone; default the last 90 days, at most 731): open " +
			"pipeline value by stage (now), deals won and lost with win rate and average won deal, stage-to-stage conversion for " +
			"the deals opened in the range (by the furthest stage each reached), won/lost per day, week or month, lead sources " +
			"(needs leads:read on the token, else null) and inbox first-response time (needs inbox:read, else null). Money is in " +
			"minor units of the workspace's currency; deals in other currencies are counted in other_currency_*, never converted. " +
			"Rates are 0..1 and null when there is nothing to divide by.",
		Scope: "contacts:read",
		InputSchema: schema(nil, map[string]any{
			"from": prop("string", "First day, YYYY-MM-DD (default: 89 days before to)"),
			"to":   prop("string", "Last day, inclusive, YYYY-MM-DD (default: today in the workspace's time zone)"),
			"days": prop("integer", "Without from: the window of this many days ending on to, e.g. 30 (default 90)"),
		}),
		Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
			args := decodeArgs(raw)
			rep, err := reports.CRMFor(ctx, a.SQLDB, reports.Range{From: str(args, "from"), To: str(args, "to"), Days: intArg(args, "days", 0)}, time.Now())
			if err != nil {
				return "", err
			}
			return jsonResult(rep)
		},
	}}
}
