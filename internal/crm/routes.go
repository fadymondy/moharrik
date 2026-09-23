package crm

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// TimelineOut is the first page of a record's timeline, embedded in its page.
type TimelineOut struct {
	Activities []ActivityOut `json:"activities"`
	// Every activity on the timeline; more than len(activities) means older
	// ones are behind activities_next_cursor (GET /api/w/activities).
	ActivitiesTotal      int64  `json:"activities_total"`
	ActivitiesNextCursor string `json:"activities_next_cursor"`
	// The linked inbox conversations in the first page's stretch of time
	// (MH-379); older ones come with the activity pages that reach them.
	Conversations []ConversationRef `json:"conversations" doc:"Inbox conversations linked to this record whose latest message is in the first page's stretch of time; GET /api/w/activities pages carry the older ones. Empty without inbox:read."`
}

func firstTimelinePage(ctx context.Context, db *sql.DB, s *tenant.Scope, where, id string) (TimelineOut, error) {
	p, err := timelinePage(ctx, db, s.WorkspaceID, where, id, "", DefaultTimelinePage, canReadInbox(s))
	if err != nil {
		return TimelineOut{}, err
	}
	return TimelineOut{Activities: p.Activities, ActivitiesTotal: p.Total, ActivitiesNextCursor: p.NextCursor, Conversations: p.Conversations}, nil
}

// The record pages, named so the OpenAPI schema has one name for each.
type DealDetailOut struct {
	Deal DealOut `json:"deal"`
	TimelineOut
}

type CompanyDetailOut struct {
	Company  CompanyOut   `json:"company"`
	Contacts []ContactRef `json:"contacts"`
	RelatedDealsOut
	TimelineOut
}

type ContactPageOut struct {
	Contact ContactDetail `json:"contact"`
	RelatedDealsOut
	TimelineOut
}

// RelatedDealsOut is a contact's or company's deals: at most MaxRelatedDeals,
// and the total, so a page past that says so.
type RelatedDealsOut struct {
	Deals      []DealOut `json:"deals"`
	DealsTotal int64     `json:"deals_total"`
}

type idPath struct {
	wsHeader
	ID string `path:"id"`
}

type stageIn struct {
	ID   string `json:"id,omitempty" doc:"Existing stage id; omit to add a stage."`
	Name string `json:"name" minLength:"1" maxLength:"40"`
	Kind string `json:"kind" enum:"open,won,lost"`
}

// Register mounts the CRM endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	tags := []string{"CRM"}
	db := func() *sql.DB { return a.SQLDB }

	// ---- pipeline ------------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-pipeline-stages", Method: http.MethodGet, Path: "/api/w/pipeline/stages",
		Summary: "The pipeline's stages in order, with deal counts and totals (seeded on first use)", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Stages []StageOut `json:"stages"`
	}], error) {
		s, err := scope(ctx, authz.ContactsRead)
		if err != nil {
			return nil, err
		}
		if err := EnsureStages(ctx, db(), s.WorkspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not prepare the pipeline")
		}
		st, err := listStages(ctx, db(), s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list stages")
		}
		return out(struct {
			Stages []StageOut `json:"stages"`
		}{st}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-put-pipeline-stages", Method: http.MethodPut, Path: "/api/w/pipeline/stages",
		Summary: "Replace the pipeline: rename, reorder, add and remove stages (a stage holding deals cannot be removed)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Stages []stageIn `json:"stages" minItems:"1" maxItems:"20"`
		}
	}) (*body[struct {
		Stages []StageOut `json:"stages"`
	}], error) {
		s, err := scope(ctx, authz.PipelineManage)
		if err != nil {
			return nil, err
		}
		open := false
		for _, st := range in.Body.Stages {
			if strings.TrimSpace(st.Name) == "" {
				return nil, huma.Error422UnprocessableEntity("every stage needs a name")
			}
			open = open || st.Kind == "open"
		}
		if !open {
			return nil, huma.Error422UnprocessableEntity("the pipeline needs at least one open stage")
		}
		if err := EnsureStages(ctx, db(), s.WorkspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not prepare the pipeline")
		}
		tx, err := db().BeginTx(ctx, nil)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save the pipeline")
		}
		defer tx.Rollback() //nolint:errcheck
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "crm-stages:"+s.WorkspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not save the pipeline")
		}
		keep := map[string]bool{}
		for i, st := range in.Body.Stages {
			name := strings.TrimSpace(st.Name)
			if st.ID != "" {
				res, err := tx.ExecContext(ctx, `UPDATE pipeline_stages SET name = $3, kind = $4, position = $5, updated_at = now()
					WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, st.ID, name, st.Kind, i)
				if err != nil {
					return nil, huma.Error500InternalServerError("could not save the pipeline")
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return nil, huma.Error422UnprocessableEntity("stage " + st.ID + " is not in this pipeline")
				}
				keep[st.ID] = true
				continue
			}
			var id string
			if err := tx.QueryRowContext(ctx, `INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, $2, $3, $4) RETURNING id`,
				s.WorkspaceID, name, i, st.Kind).Scan(&id); err != nil {
				return nil, huma.Error500InternalServerError("could not save the pipeline")
			}
			keep[id] = true
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, name FROM pipeline_stages WHERE workspace_id = $1`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save the pipeline")
		}
		var drop [][2]string
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err == nil && !keep[id] {
				drop = append(drop, [2]string{id, name})
			}
		}
		rows.Close()
		for _, d := range drop {
			if _, err := tx.ExecContext(ctx, `DELETE FROM pipeline_stages WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, d[0]); err != nil {
				if pgCode(err) == "23503" {
					return nil, huma.Error409Conflict("move the deals out of \"" + d[1] + "\" before removing it")
				}
				return nil, huma.Error500InternalServerError("could not save the pipeline")
			}
		}
		// A stage that changed kind opens or closes the deals in it.
		if _, err := tx.ExecContext(ctx, `
			UPDATE deals d SET closed_at = CASE WHEN st.kind = 'open' THEN NULL ELSE coalesce(d.closed_at, now()) END
			FROM pipeline_stages st
			WHERE d.workspace_id = $1 AND st.workspace_id = $1 AND st.id = d.stage_id
			  AND (st.kind = 'open') = (d.closed_at IS NOT NULL)`, s.WorkspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not save the pipeline")
		}
		if err := tx.Commit(); err != nil {
			return nil, huma.Error500InternalServerError("could not save the pipeline")
		}
		st, err := listStages(ctx, db(), s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list stages")
		}
		return out(struct {
			Stages []StageOut `json:"stages"`
		}{st}), nil
	})

	// ---- deals ---------------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-deals", Method: http.MethodGet, Path: "/api/w/deals",
		Summary: "The pipeline and its deals, a page at a time (board and list views)",
		Description: "view=board (default) sends the first `limit` deals of EVERY stage; a stage with more carries " +
			"next_cursor, and ?stage_id=&cursor= fetches that stage's next page. view=list pages the whole pipeline in " +
			"board order. `total` is every deal the view covers. A limit over 500 is refused, not clamped (MH-467).",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		View    string `query:"view" enum:"board,list," doc:"board (default) or list"`
		StageID string `query:"stage_id" doc:"Board: one stage's next page (with cursor)"`
		Cursor  string `query:"cursor" doc:"next_cursor from the previous page (or a stage's)"`
		Limit   int    `query:"limit" minimum:"0" maximum:"500" doc:"Board: deals per stage (default 100). List: page size (default 50). At most 500."`
	}) (*body[DealsPage], error) {
		page, err := DealsPageFor(ctx, db(), DealsQuery{View: in.View, StageID: in.StageID, Cursor: in.Cursor, Limit: in.Limit})
		if err != nil {
			return nil, err
		}
		return out(*page), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-deal", Method: http.MethodPost, Path: "/api/w/deals",
		DefaultStatus: http.StatusCreated, Summary: "Open a deal (first open stage, workspace currency and you as owner unless given)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body DealFields
	}) (*body[struct {
		Deal DealOut `json:"deal"`
	}], error) {
		d, err := CreateDeal(ctx, db(), in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Deal DealOut `json:"deal"`
		}{*d}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-deal", Method: http.MethodGet, Path: "/api/w/deals/{id}",
		Summary: "One deal and its timeline", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[DealDetailOut], error) {
		d, err := GetDealDetail(ctx, db(), in.ID)
		if err != nil {
			return nil, err
		}
		return out(*d), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-deal", Method: http.MethodPatch, Path: "/api/w/deals/{id}",
		Summary: "Change a deal — including moving it to another stage (the board's drag)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body DealFields
	}) (*body[struct {
		Deal DealOut `json:"deal"`
	}], error) {
		d, err := UpdateDeal(ctx, db(), in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Deal DealOut `json:"deal"`
		}{*d}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-deal", Method: http.MethodDelete, Path: "/api/w/deals/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a deal and its activities", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return deleteRow(ctx, db(), "deals", in.ID, "deal")
	})

	// ---- companies -----------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-companies", Method: http.MethodGet, Path: "/api/w/companies",
		Summary: "Companies with their contact and open-deal counts, a page at a time",
		Description: "A-Z. `total` is every company matching `q`; follow `next_cursor` for the next page. " +
			"A limit over 200 is refused, not clamped (MH-467).",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Q      string `query:"q" maxLength:"200" doc:"Search name and domain"`
		Cursor string `query:"cursor" doc:"next_cursor from the previous page"`
		Limit  int    `query:"limit" minimum:"0" maximum:"200" doc:"Page size (default 50, at most 200)"`
	}) (*body[CompanyPage], error) {
		page, err := CompaniesPage(ctx, db(), in.Q, in.Cursor, in.Limit)
		if err != nil {
			return nil, err
		}
		return out(*page), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-company", Method: http.MethodPost, Path: "/api/w/companies",
		DefaultStatus: http.StatusCreated, Summary: "Add a company (one per domain)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body CompanyFields
	}) (*body[struct {
		Company CompanyOut `json:"company"`
	}], error) {
		c, err := CreateCompany(ctx, db(), in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Company CompanyOut `json:"company"`
		}{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-company", Method: http.MethodGet, Path: "/api/w/companies/{id}",
		Summary: "One company: its people, deals and timeline", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[CompanyDetailOut], error) {
		d, err := GetCompanyDetail(ctx, db(), in.ID)
		if err != nil {
			return nil, err
		}
		return out(*d), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-company", Method: http.MethodPatch, Path: "/api/w/companies/{id}",
		Summary: "Change a company", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body CompanyFields
	}) (*body[struct {
		Company CompanyOut `json:"company"`
	}], error) {
		c, err := UpdateCompany(ctx, db(), in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Company CompanyOut `json:"company"`
		}{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-company", Method: http.MethodDelete, Path: "/api/w/companies/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a company (its people and deals stay, unlinked)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return deleteRow(ctx, db(), "companies", in.ID, "company")
	})

	// ---- contacts (CRM view of the marketing module's table) -----------------

	huma.Register(api, huma.Operation{
		OperationID: "w-contact", Method: http.MethodGet, Path: "/api/w/contacts/{id}",
		Summary: "One contact: CRM fields, list subscriptions, deals and timeline", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[ContactPageOut], error) {
		c, err := GetContactPage(ctx, db(), in.ID)
		if err != nil {
			return nil, err
		}
		return out(*c), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-contact", Method: http.MethodPatch, Path: "/api/w/contacts/{id}",
		Summary: "Change a contact's CRM fields: name, phone, title, company, owner, tags", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body ContactFields
	}) (*body[struct {
		Contact ContactDetail `json:"contact"`
	}], error) {
		c, err := UpdateContact(ctx, db(), in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Contact ContactDetail `json:"contact"`
		}{*c}), nil
	})

	// ---- activities ----------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-activities", Method: http.MethodGet, Path: "/api/w/activities",
		Summary: "One record's timeline, a page at a time (newest first)",
		Description: "Exactly one of contact_id, company_id or deal_id. The record pages (GET /api/w/contacts/{id} etc.) " +
			"carry the first page; follow `next_cursor` here for older ones. A limit over 200 is refused (MH-467).",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `query:"contact_id"`
		CompanyID string `query:"company_id"`
		DealID    string `query:"deal_id"`
		Cursor    string `query:"cursor" doc:"next_cursor from the previous page"`
		Limit     int    `query:"limit" minimum:"0" maximum:"200" doc:"Page size (default 50, at most 200)"`
	}) (*body[ActivityPage], error) {
		page, err := TimelinePage(ctx, db(), TimelineQuery{ContactID: in.ContactID, CompanyID: in.CompanyID, DealID: in.DealID, Cursor: in.Cursor, Limit: in.Limit})
		if err != nil {
			return nil, err
		}
		return out(*page), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-activity", Method: http.MethodPost, Path: "/api/w/activities",
		DefaultStatus: http.StatusCreated, Summary: "Log a note, call or meeting, or add a task, on a contact, company and/or deal", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body ActivityInput
	}) (*body[struct {
		Activity ActivityOut `json:"activity"`
	}], error) {
		act, err := CreateActivity(ctx, db(), in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Activity ActivityOut `json:"activity"`
		}{*act}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-activity", Method: http.MethodPatch, Path: "/api/w/activities/{id}",
		Summary: "Edit an activity or tick a task done", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Body  *string `json:"body,omitempty" maxLength:"5000"`
			Done  *bool   `json:"done,omitempty" doc:"Tasks only."`
			DueAt *string `json:"due_at,omitempty" doc:"Tasks only; empty string clears."`
		}
	}) (*body[struct {
		Activity ActivityOut `json:"activity"`
	}], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		b := in.Body
		u := &update{}
		if b.Body != nil {
			t := strings.TrimSpace(*b.Body)
			if t == "" {
				return nil, huma.Error422UnprocessableEntity("write something")
			}
			u.set("body", t)
		}
		if b.Done != nil {
			if *b.Done {
				u.sets = append(u.sets, "done_at = coalesce(done_at, now())")
			} else {
				u.sets = append(u.sets, "done_at = NULL")
			}
		}
		if b.DueAt != nil {
			v, err := parseTime(*b.DueAt)
			if err != nil {
				return nil, err
			}
			u.set("due_at", v)
		}
		if len(u.sets) > 0 {
			// Done and due dates only mean something on a task.
			q := `UPDATE activities SET ` + strings.Join(u.sets, ", ") + `, updated_at = now() WHERE workspace_id = $1 AND id = $2`
			if b.Done != nil || b.DueAt != nil {
				q += ` AND kind = 'task'`
			}
			res, err := db().ExecContext(ctx, q, append([]any{s.WorkspaceID, in.ID}, u.args...)...)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not update the activity")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("task not found")
			}
		}
		act, err := getActivity(ctx, db(), s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("activity not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the activity")
		}
		return out(struct {
			Activity ActivityOut `json:"activity"`
		}{*act}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-activity", Method: http.MethodDelete, Path: "/api/w/activities/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete an activity (its author, or a manager)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		var author string
		err = db().QueryRowContext(ctx, `SELECT created_by FROM activities WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID).Scan(&author)
		if notFound(err) {
			return nil, huma.Error404NotFound("activity not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the activity")
		}
		if author != s.UserID && !authz.Role(s.Role).AtLeast(authz.Manager) {
			return nil, huma.Error403Forbidden("only its author or a manager can delete an activity")
		}
		return deleteRow(ctx, db(), "activities", in.ID, "activity")
	})
}

// deleteRow deletes one row of a CRM table in the caller's workspace. table is
// always a constant from this file, never input.
func deleteRow(ctx context.Context, db *sql.DB, table, id, noun string) (*struct{}, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not delete the " + noun)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, huma.Error404NotFound(noun + " not found")
	}
	return &struct{}{}, nil
}
