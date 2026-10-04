package home

import "database/sql"

// google counts the workspace's Google connections and the enabled resources hanging off them.
// nil when none exist: nothing configured, nothing shown. Emails and error text stay out.
func (b *builder) google() *HomeGoogle {
	g := &HomeGoogle{Services: []HomeGoogleKind{}}
	b.each(`SELECT count(*), coalesce(bool_or(status <> '' OR auth_error <> ''), false) FROM google_connections WHERE workspace_id = $1`,
		nil, func(r *sql.Rows) error { return r.Scan(&g.Connections, &g.HasError) })
	if b.err != nil || g.Connections == 0 {
		return nil
	}
	b.each(`SELECT kind, count(*) FROM google_resources WHERE workspace_id = $1 AND enabled GROUP BY kind ORDER BY kind`,
		nil, func(r *sql.Rows) error {
			var k HomeGoogleKind
			if err := r.Scan(&k.Kind, &k.Count); err != nil {
				return err
			}
			g.Services = append(g.Services, k)
			return nil
		})
	return g
}
