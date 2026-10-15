package circlexo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/circlexo/circlexo-go/entitlements"
)

/*
Entitlements for a hub-linked workspace.

Moharrik is free and open source (2026-10-15): nothing is sold and nothing is gated. The hub's
entitlements for a workspace with an ACTIVE org link are still read and shown (GET /api/w/circlexo,
api.go) so an owner can see the link, but Moharrik refuses nothing because of them: not campaign
sends, not invitations, not when the hub is down or says the org has no entitlements. No usage is
reported. A workspace with no link, or an inactive one, is never touched.
*/

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
