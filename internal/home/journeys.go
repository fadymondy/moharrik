package home

import (
	"database/sql"
	"time"
)

// journeys: the pipeline's open and won stages in order, and the JourneyLimit open deals that
// moved most recently, each placed on that pipeline. Quiet is the same 7-day rule as at-risk.
func (b *builder) journeys() *HomeJourneys {
	j := &HomeJourneys{Stages: []HomeStage{}, Deals: []HomeJourney{}}
	idx := map[string]int{}
	b.each(`SELECT id, name, kind FROM pipeline_stages WHERE workspace_id = $1 AND kind IN ('open', 'won')
		ORDER BY CASE kind WHEN 'open' THEN 0 ELSE 1 END, position, id`, nil, func(r *sql.Rows) error {
		var s HomeStage
		if err := r.Scan(&s.ID, &s.Name, &s.Kind); err != nil {
			return err
		}
		idx[s.ID] = len(j.Stages)
		j.Stages = append(j.Stages, s)
		return nil
	})
	b.each(`
		SELECT d.id, d.title, d.value_minor, d.currency, d.stage_id, coalesce(ct.name, ''), coalesce(co.name, ''), x.at
		FROM deals d
		JOIN pipeline_stages s ON s.workspace_id = d.workspace_id AND s.id = d.stage_id AND s.kind = 'open'
		LEFT JOIN contacts ct ON ct.workspace_id = d.workspace_id AND ct.id = d.contact_id
		LEFT JOIN companies co ON co.workspace_id = d.workspace_id AND co.id = d.company_id
		CROSS JOIN LATERAL (SELECT `+lastActivitySQL+` AS at) x
		WHERE d.workspace_id = $1
		ORDER BY x.at DESC, d.id
		LIMIT `+itoa(JourneyLimit), nil, func(r *sql.Rows) error {
		var d HomeJourney
		var stageID string
		if err := r.Scan(&d.ID, &d.Title, &d.Value.Minor, &d.Value.Currency, &stageID, &d.Contact, &d.Company, &d.LastActivity); err != nil {
			return err
		}
		d.StageIndex = idx[stageID]
		d.StageName = j.Stages[d.StageIndex].Name
		age := b.now.Sub(d.LastActivity)
		if age < 0 {
			age = 0
		}
		d.DaysQuiet = int(age / (24 * time.Hour))
		d.Quiet = age >= QuietAfter
		j.Deals = append(j.Deals, d)
		return nil
	})
	return j
}
