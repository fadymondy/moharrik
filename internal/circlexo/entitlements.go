package circlexo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/circlexo/circlexo-go/entitlements"
	"github.com/danielgtaylor/huma/v2"
)

/*
Entitlements for a hub-linked workspace.

Moharrik has no plan engine of its own (workspaces.plan is a label nothing reads), so there is
nothing to overlay: the hub's entitlements are read for a workspace with an ACTIVE org link and
exposed (GET /api/w/circlexo, api.go). They are ENFORCED only where Moharrik already has a natural
place to say no:

  - `campaigns` (flag) and `emails_sent` (meter) gate starting a campaign send (marketing.StartSend,
    which the REST route and the send_campaign MCP tool share): no `campaigns`, an inactive or
    read-only subscription, or an `emails_sent` allowance that is used up without pay-as-you-go
    overage each refuse the send.
  - `members` (limit) caps invitations (workspaces.Invite): with a limit, no invitation is created
    once the workspace has that many active members.

Every other hub feature (inbox, crm, automation, research, trends) and the `contacts` meter are
exposed and, for the meters, reported, but not blocked: Moharrik has no single choke point for
them, and gating half of a page's routes would be worse than not gating. A workspace with no
link, or an inactive one, is never touched, and nothing runs when the integration is off.

Fail closed, like Mahaam: when the hub cannot confirm the org's entitlements (or says it has
none) the gated actions above are refused. In the SDK's grace window (a stale copy while the hub is
down) the last known entitlements apply but Set.ReadOnly is set, which also refuses them; reading
keeps working.
*/

// Hub feature and meter keys, as declared in the manifest.
const (
	FeatMembers   = "members"
	FeatCampaigns = "campaigns"
	MeterEmails   = "emails_sent"
	MeterContacts = "contacts"
)

// State is what the hub says about a workspace. Link is nil when the workspace is not governed by
// the hub (no link, or an inactive one). Set is nil when the hub could not confirm the
// entitlements (the fail-closed case).
type State struct {
	Link *OrgLink
	Set  *entitlements.Set
}

// Governed reports whether the hub governs the workspace.
func (st State) Governed() bool { return st.Link != nil }

// StateOf reads the link and the entitlements of a workspace.
func (s *Service) StateOf(ctx context.Context, workspaceID string) (State, error) {
	link, err := s.OrgByWorkspace(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !link.Active) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	set, err := s.Ents.Get(ctx, link.OrgID, 0)
	if err != nil {
		set = nil // unreachable with no usable copy, or the hub says the org does not have the app
	}
	return State{Link: link, Set: set}, nil
}

func deny(msg string) error { return huma.Error403Forbidden(msg) }

// gate is the common refusal: the hub-governed workspace has no usable subscription.
func (st State) gate() error {
	switch {
	case st.Set == nil || !st.Set.Active:
		return deny("this workspace's CircleXO subscription is not active")
	case st.Set.ReadOnly:
		return deny("CircleXO cannot confirm this workspace's subscription right now; try again shortly")
	}
	return nil
}

// beforeCampaignSend is marketing.BeforeCampaignSend.
func (s *Service) beforeCampaignSend(ctx context.Context, workspaceID string) error {
	st, err := s.StateOf(ctx, workspaceID)
	if err != nil {
		return huma.Error500InternalServerError("could not check the subscription")
	}
	if !st.Governed() {
		return nil
	}
	if err := st.gate(); err != nil {
		return err
	}
	if !st.Set.Has(FeatCampaigns) {
		return deny("this workspace's CircleXO plan does not include campaigns")
	}
	if f, ok := st.Set.Feature(MeterEmails); ok && f.Limit != nil && !st.Set.PaygEnabled && f.UsedInPeriod >= *f.Limit {
		return deny("this period's email allowance is used up; add pay-as-you-go or upgrade in CircleXO")
	}
	return nil
}

// beforeInvite is workspaces.BeforeInvite.
func (s *Service) beforeInvite(ctx context.Context, workspaceID string) error {
	st, err := s.StateOf(ctx, workspaceID)
	if err != nil {
		return huma.Error500InternalServerError("could not check the subscription")
	}
	if !st.Governed() {
		return nil
	}
	if err := st.gate(); err != nil {
		return err
	}
	limit, ok := st.Set.Limit(FeatMembers)
	if !ok {
		if !st.Set.Has(FeatMembers) {
			return deny("this workspace's CircleXO plan does not include team members")
		}
		return nil // enabled, no limit
	}
	var n int64
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND status = 'active'`, workspaceID).Scan(&n); err != nil {
		return huma.Error500InternalServerError("could not count the members")
	}
	if n >= limit {
		return deny("this workspace's CircleXO plan allows no more members")
	}
	return nil
}
