// MH-591: Contacts (and their identities, meta and Person-parity fields)
// were missing from the GraphQL schema. Every resolver here calls the exact
// same internal/crm and internal/marketing functions REST and the MCP tools
// call (crm.GetContact, crm.UpdateContact, crm.LinkContactIdentity,
// crm.UnlinkContactIdentity, marketing.AddContact, marketing.DeleteContact,
// marketing.ListContactsPage) — so authz, validation and RLS all come from
// one place, and a huma error surfaces as the same message over GraphQL as
// it does over REST (huma.ErrorModel.Error() returns e.Detail verbatim).
//
// No new SQL was written for this: the only genuinely new code is the
// crm.GetContact (see service.go) and marketing.DeleteContact (see
// marketing/handlers.go) wrappers, both extracted so GraphQL, REST and MCP
// share one implementation instead of three.
package resolvers

import (
	"context"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	graphgen "github.com/fadymondy/moharrik/internal/graph/gen"
	"github.com/fadymondy/moharrik/internal/graph/model"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// scope is the same duplicated-per-package pattern internal/crm and
// internal/marketing already use (there is no shared helper to call instead):
// resolve the tenant off the context, then check the role can do the action.
// marketing.ListContactsPage takes a caller that has already checked this —
// unlike crm.GetContact/UpdateContact/etc., which check it themselves.
func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

// toModelContact converts the shared crm.ContactDetail DTO to the GraphQL
// model. It is the one place the two shapes are reconciled, so every
// resolver below returns identical field values for identical contacts.
func toModelContact(c *crm.ContactDetail) *model.Contact {
	if c == nil {
		return nil
	}
	out := &model.Contact{
		ID:                 c.ID,
		Name:               c.Name,
		Email:              c.Email,
		Phone:              c.Phone,
		Handle:             c.Handle,
		Title:              c.Title,
		OwnerUserID:        c.OwnerUserID,
		Tags:               c.Tags,
		Lifecycle:          c.Lifecycle,
		Locale:             c.Locale,
		GivenName:          c.GivenName,
		FamilyName:         c.FamilyName,
		MiddleName:         c.MiddleName,
		NamePrefix:         c.NamePrefix,
		NameSuffix:         c.NameSuffix,
		PhoneticGivenName:  c.PhoneticGivenName,
		PhoneticFamilyName: c.PhoneticFamilyName,
		Nickname:           c.Nickname,
		Birthday:           c.Birthday,
		Biography:          c.Biography,
		AvatarURL:          c.AvatarURL,
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
	}
	if c.Company != nil {
		out.CompanyID = &c.Company.ID
	}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	// Meta travels as a sorted list (GraphQL has no map scalar) — sorted so
	// the order is stable across requests instead of Go's random map order.
	keys := make([]string, 0, len(c.Meta))
	for k := range c.Meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out.Meta = make([]*model.ContactMetaEntry, 0, len(keys))
	for _, k := range keys {
		out.Meta = append(out.Meta, &model.ContactMetaEntry{Key: k, Value: c.Meta[k]})
	}
	out.Identities = make([]*model.ContactIdentity, 0, len(c.Identities))
	for _, id := range c.Identities {
		id := id
		out.Identities = append(out.Identities, &model.ContactIdentity{
			ID:         id.ID,
			Channel:    id.Channel,
			ExternalID: id.ExternalID,
			Label:      id.Label,
			IsPrimary:  id.IsPrimary,
		})
	}
	return out
}

// contactFields turns UpdateContactInput's nil-means-unchanged pointers into
// crm.ContactFields' identical pointer semantics — a straight field copy,
// not a reinterpretation, so "meta replaces the whole map" and "empty
// string clears it" behave exactly as they do for REST's PATCH and the
// update_contact MCP tool.
func contactFields(in graphgen.UpdateContactInput) crm.ContactFields {
	f := crm.ContactFields{
		Email:              in.Email,
		Name:               in.Name,
		Phone:              in.Phone,
		Handle:             in.Handle,
		Title:              in.Title,
		CompanyID:          in.CompanyID,
		OwnerUserID:        in.OwnerUserID,
		Lifecycle:          in.Lifecycle,
		GivenName:          in.GivenName,
		FamilyName:         in.FamilyName,
		MiddleName:         in.MiddleName,
		NamePrefix:         in.NamePrefix,
		NameSuffix:         in.NameSuffix,
		PhoneticGivenName:  in.PhoneticGivenName,
		PhoneticFamilyName: in.PhoneticFamilyName,
		Nickname:           in.Nickname,
		Birthday:           in.Birthday,
		Biography:          in.Biography,
	}
	if in.Tags != nil {
		tags := in.Tags
		f.Tags = &tags
	}
	if in.Meta != nil {
		m := make(map[string]string, len(in.Meta))
		for _, e := range in.Meta {
			if e != nil {
				m[e.Key] = e.Value
			}
		}
		f.Meta = &m
	}
	return f
}

func (r *queryResolver) Contact(ctx context.Context, id string) (*model.Contact, error) {
	c, err := crm.GetContact(ctx, r.App.SQLDB, id)
	if err != nil {
		return nil, err
	}
	return toModelContact(c), nil
}

// Contacts lists contacts via marketing.ListContactsPage (the same search,
// pagination and total marketing/CRM list views already use), then hydrates
// each row to a full Contact via crm.GetContact so list and single-contact
// results carry identical fields — including meta and identities, which
// marketing.ContactOut does not (that lighter struct only serves the mail
// list view). Default/limit page sizes are 50/200 (marketing.DefaultContactPage,
// MaxContactPage), so the N+1 read here stays bounded.
func (r *queryResolver) Contacts(ctx context.Context, search *string, limit *int, cursor *string) (*graphgen.ContactPage, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	f := marketing.ContactFilter{}
	if search != nil {
		f.Q = *search
	}
	l := 0
	if limit != nil {
		l = *limit
	}
	after := ""
	if cursor != nil {
		after = *cursor
	}
	page, err := marketing.ListContactsPage(ctx, r.App.SQLDB, s.WorkspaceID, f, after, l)
	if err != nil {
		return nil, err
	}
	out := &graphgen.ContactPage{
		Contacts: make([]*model.Contact, 0, len(page.Contacts)),
		Total:    int(page.Total),
		Limit:    page.Limit,
	}
	if page.NextCursor != "" {
		nc := page.NextCursor
		out.NextCursor = &nc
	}
	for _, row := range page.Contacts {
		c, err := crm.GetContact(ctx, r.App.SQLDB, row.ID)
		if err != nil {
			return nil, err
		}
		out.Contacts = append(out.Contacts, toModelContact(c))
	}
	return out, nil
}

func (r *mutationResolver) CreateContact(ctx context.Context, input graphgen.CreateContactInput) (*model.Contact, error) {
	in := marketing.AddContactInput{}
	if input.Email != nil {
		in.Email = *input.Email
	}
	if input.Phone != nil {
		in.Phone = *input.Phone
	}
	if input.Handle != nil {
		in.Handle = *input.Handle
	}
	if input.Name != nil {
		in.Name = *input.Name
	}
	if input.Locale != nil {
		in.Locale = *input.Locale
	}
	if input.ListID != nil {
		in.ListID = *input.ListID
	}
	res, err := marketing.AddContact(ctx, r.App, in)
	if err != nil {
		return nil, err
	}
	id := res.Contact.ID
	// Title/CompanyID are CRM-only fields AddContact does not take (it only
	// knows the marketing side) — same follow-up UpdateContact the
	// create_contact MCP tool makes when either is given.
	if input.Title != nil || input.CompanyID != nil {
		if _, err := crm.UpdateContact(ctx, r.App.SQLDB, id, crm.ContactFields{Title: input.Title, CompanyID: input.CompanyID}); err != nil {
			return nil, err
		}
	}
	c, err := crm.GetContact(ctx, r.App.SQLDB, id)
	if err != nil {
		return nil, err
	}
	return toModelContact(c), nil
}

func (r *mutationResolver) UpdateContact(ctx context.Context, id string, input graphgen.UpdateContactInput) (*model.Contact, error) {
	c, err := crm.UpdateContact(ctx, r.App.SQLDB, id, contactFields(input))
	if err != nil {
		return nil, err
	}
	return toModelContact(c), nil
}

func (r *mutationResolver) DeleteContact(ctx context.Context, id string) (bool, error) {
	if err := marketing.DeleteContact(ctx, r.App, id); err != nil {
		return false, err
	}
	return true, nil
}

func (r *mutationResolver) LinkContactIdentity(ctx context.Context, contactID string, input graphgen.LinkContactIdentityInput) (*model.Contact, error) {
	in := crm.LinkIdentityInput{Channel: input.Channel, ExternalID: input.ExternalID}
	if input.Label != nil {
		in.Label = *input.Label
	}
	if input.IsPrimary != nil {
		in.IsPrimary = *input.IsPrimary
	}
	c, err := crm.LinkContactIdentity(ctx, r.App.SQLDB, contactID, in)
	if err != nil {
		return nil, err
	}
	return toModelContact(c), nil
}

func (r *mutationResolver) UnlinkContactIdentity(ctx context.Context, contactID string, identityID string) (*model.Contact, error) {
	c, err := crm.UnlinkContactIdentity(ctx, r.App.SQLDB, contactID, identityID)
	if err != nil {
		return nil, err
	}
	return toModelContact(c), nil
}

func (r *mutationResolver) MergeContact(ctx context.Context, contactID string, sourceID string) (*model.Contact, error) {
	c, err := crm.MergeContact(ctx, r.App.SQLDB, contactID, crm.MergeContactInput{SourceID: sourceID})
	if err != nil {
		return nil, err
	}
	return toModelContact(c), nil
}
