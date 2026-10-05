package circlexo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	circlexo "github.com/circlexo/circlexo-go"

	"github.com/fadymondy/moharrik/internal/workspaces"
)

/*
Org -> workspace, and member sync.

A hub org maps to exactly one workspace (circlexo_org_links). Install (the org.app_installed
webhook, or the provision endpoint — both land in Provision) creates the workspace or reuses the
one already linked, adds the hub's members, and confirms the tenant to the hub. Nothing here
deletes anything: removing the app marks the link inactive and removing a member suspends the
membership; the hub can undo both by installing/adding again.

Roles. The hub knows owner, admin, billing and member; a Moharrik workspace also has manager and
client, which the hub cannot express. So the hub decides the top of the ladder and leaves the rest:

	hub owner -> owner            hub admin -> admin
	hub member, hub billing -> member for someone new; an existing manager/member/client keeps
	its Moharrik role; an existing owner/admin is demoted to member (the hub took it away)

and the last owner of a workspace is never demoted or suspended by a sync.
*/

// mapRole is the Moharrik role a hub role asks for.
func mapRole(hubRole string) string {
	switch strings.ToLower(hubRole) {
	case "owner":
		return "owner"
	case "admin":
		return "admin"
	}
	return "member"
}

// ProvisionInput is what an install carries. The org's own details and members are read from the
// hub (Client.Org); the event's copies are the fallback.
type ProvisionInput struct {
	OrgID  string
	Slug   string
	Name   string
	Locale string
	// SignIn is set when a member's sign-in (not the hub's install) asks for the workspace. A
	// sign-in only creates one while the hub is still provisioning the install; once the install
	// is active, a missing workspace means someone deleted it in Moharrik (MH-1149).
	SignIn bool
}

// ErrReleased: the org's workspace was deleted in Moharrik, so a sign-in does not make a new one.
// The hub has been told (Client.ReleaseTenant) and shows the app as not installed; installing it
// again from the hub provisions a fresh workspace.
var ErrReleased = errors.New("circlexo: the org's workspace was deleted in Moharrik")

// Provision is idempotent: any number of calls for one org leave one workspace, one link and the
// current members, and always end by confirming the tenant.
func (s *Service) Provision(ctx context.Context, in ProvisionInput) (*OrgLink, error) {
	if in.OrgID == "" {
		return nil, errors.New("circlexo: an org id is required")
	}
	org, err := s.Client.Org(ctx, in.OrgID)
	if err != nil {
		return nil, fmt.Errorf("circlexo: reading the org from the hub: %w", err)
	}
	in.Slug = firstNonEmpty(org.Slug, in.Slug)
	in.Name = firstNonEmpty(org.Name, in.Name)
	in.Locale = firstNonEmpty(org.DefaultLocale, in.Locale)

	link, err := s.OrgByID(ctx, in.OrgID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if link, err = s.relink(ctx, in, org.Tenant.ProductTenantID); err != nil {
			return nil, err
		}
		if link != nil {
			break
		}
		if in.SignIn && org.Tenant.Status != "provisioning" {
			if org.Tenant.ProductTenantID != "" {
				if _, err := s.Client.ReleaseTenant(ctx, in.OrgID, org.Tenant.ProductTenantID); err != nil {
					s.Log.Warn("circlexo: releasing a deleted workspace's tenant", "org", in.OrgID, "err", err)
				}
			}
			return nil, ErrReleased
		}
		owner, err := s.ownerFor(ctx, org.Members)
		if err != nil {
			return nil, err
		}
		if link, err = s.createLinked(ctx, in, owner); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case !link.Active || (in.Slug != "" && link.OrgSlug != in.Slug):
		slug := firstNonEmpty(in.Slug, link.OrgSlug)
		if _, err := s.DB.ExecContext(ctx, `UPDATE circlexo_org_links SET status = 'active', org_slug = $2, updated_at = now() WHERE org_id = $1`, in.OrgID, slug); err != nil {
			return nil, err
		}
		link.Active, link.OrgSlug = true, slug
	}

	if err := s.syncMembers(ctx, link.WorkspaceID, org.Members); err != nil {
		return nil, err
	}
	if _, err := s.Client.ConfirmTenant(ctx, in.OrgID, link.WorkspaceID, link.WorkspaceSlug); err != nil {
		return nil, fmt.Errorf("circlexo: confirming the tenant: %w", err)
	}
	s.Ents.Invalidate(in.OrgID, 0)
	s.changed(link.WorkspaceID)
	return link, nil
}

// Deprovision marks an org's link inactive. The workspace, its members and its data stay; it just
// stops being governed by the hub. An unknown org is a success (idempotent).
func (s *Service) Deprovision(ctx context.Context, orgID string) error {
	var ws string
	err := s.DB.QueryRowContext(ctx, `
		UPDATE circlexo_org_links SET status = 'inactive', updated_at = now()
		WHERE org_id = $1 RETURNING workspace_id`, orgID).Scan(&ws)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	s.Ents.Invalidate(orgID, 0)
	s.changed(ws)
	return nil
}

func (s *Service) changed(workspaceID string) {
	if s.OnChange != nil {
		s.OnChange(workspaceID)
	}
}

// ownerFor picks the Moharrik user who owns a new workspace: the first hub owner, else the first
// admin, else the first member. A person with no Moharrik account yet gets a passwordless one (the
// account they land in when they sign in with CircleXO).
func (s *Service) ownerFor(ctx context.Context, members []circlexo.Member) (string, error) {
	pick := func(role string) *circlexo.Member {
		for i := range members {
			if strings.EqualFold(members[i].Role, role) {
				return &members[i]
			}
		}
		return nil
	}
	m := pick("owner")
	if m == nil {
		m = pick("admin")
	}
	if m == nil && len(members) > 0 {
		m = &members[0]
	}
	if m == nil {
		return "", errors.New("circlexo: the org has no members to own the workspace")
	}
	id, ok, err := s.resolveMember(ctx, *m, true)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("circlexo: the owner's Moharrik account cannot be used automatically (unconfirmed address with a password)")
	}
	return id, nil
}

// relink restores a lost link to the workspace the hub already knows serves the org, when that
// workspace still exists and no other org holds it. nil, nil when there is nothing to restore.
func (s *Service) relink(ctx context.Context, in ProvisionInput, workspaceID string) (*OrgLink, error) {
	if workspaceID == "" {
		return nil, nil
	}
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO circlexo_org_links (org_id, workspace_id, org_slug, status)
		SELECT $1, w.id::text, $3, 'active' FROM workspaces w
		WHERE w.id::text = $2 AND NOT EXISTS (SELECT 1 FROM circlexo_org_links l WHERE l.workspace_id = w.id::text)
		ON CONFLICT DO NOTHING`, in.OrgID, workspaceID, in.Slug)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return s.OrgByID(ctx, in.OrgID)
}

// createLinked makes the workspace and its link in one transaction, under an advisory lock on the
// org, so two concurrent installs of one org cannot make two workspaces.
func (s *Service) createLinked(ctx context.Context, in ProvisionInput, ownerID string) (*OrgLink, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "circlexo:org:"+in.OrgID); err != nil {
		return nil, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT workspace_id FROM circlexo_org_links WHERE org_id = $1`, in.OrgID).Scan(&existing)
	if err == nil {
		return s.OrgByID(ctx, in.OrgID) // another request won: reuse its workspace
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	name := firstNonEmpty(in.Name, in.Slug, "CircleXO workspace")
	locale := "en"
	if strings.HasPrefix(strings.ToLower(in.Locale), "ar") {
		locale = "ar"
	}
	base := workspaces.Slugify(firstNonEmpty(in.Slug, name))
	// The owner already has a workspace by the org's name that no org governs (they made it
	// before installing, or an earlier link was lost): adopt it rather than making "<slug>-2".
	var wsID string
	err = tx.QueryRowContext(ctx, `
		SELECT w.id FROM workspaces w
		JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2 AND m.role = 'owner' AND m.status = 'active'
		WHERE w.slug = $1 AND NOT EXISTS (SELECT 1 FROM circlexo_org_links l WHERE l.workspace_id = w.id::text)`, base, ownerID).Scan(&wsID)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO circlexo_org_links (org_id, workspace_id, org_slug, status) VALUES ($1, $2, $3, 'active')`, in.OrgID, wsID, in.Slug); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.OrgByID(ctx, in.OrgID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for i := 0; ; i++ {
		slug := base
		switch {
		case i == 1:
			slug = trimSlug(base, 36) + "-2"
		case i > 1: // a taken slug keeps colliding (a long-lived database): stop counting
			slug = trimSlug(base, 30) + "-" + randomSuffix()
		}
		if !workspaces.ValidSlug(slug) {
			slug = "org-" + randomSuffix()
		}
		// A savepoint per attempt: a unique violation must not poison the transaction.
		if _, err := tx.ExecContext(ctx, `SAVEPOINT slug`); err != nil {
			return nil, err
		}
		err := tx.QueryRowContext(ctx,
			`INSERT INTO workspaces (slug, name, created_by, locale) VALUES ($1, $2, $3, $4) RETURNING id`,
			slug, name, ownerID, locale).Scan(&wsID)
		if err == nil {
			break
		}
		if !isUnique(err) || i > 20 {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT slug`); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, wsID, ownerID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO circlexo_org_links (org_id, workspace_id, org_slug, status) VALUES ($1, $2, $3, 'active')`, in.OrgID, wsID, in.Slug); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.OrgByID(ctx, in.OrgID)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func trimSlug(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.Trim(s, "-")
}

// resolveMember finds the Moharrik user for a hub member: by the CircleXO link, else by e-mail (see
// userByEmail for when an address alone is enough), else — with create — a new passwordless
// account. ok is false when the member cannot be placed automatically; they are picked up when
// they sign in with CircleXO. Resolving by e-mail does NOT create the link: that needs the person
// to prove the address by signing in.
func (s *Service) resolveMember(ctx context.Context, m circlexo.Member, create bool) (userID string, ok bool, err error) {
	if m.UserID != "" {
		id, err := s.UserBySubject(ctx, m.UserID)
		if err == nil {
			return id, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", false, err
		}
	}
	email := norm(m.Email)
	if email == "" {
		return "", false, nil
	}
	id, _, usable, err := s.userByEmail(ctx, email)
	switch {
	case err == nil:
		return id, usable, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", false, err
	case !create:
		return "", false, nil
	}
	ident, err := s.Auth.FindOrCreateByEmail(ctx, email)
	if err != nil {
		return "", false, err
	}
	return ident.ID, true, nil
}

// syncMembers adds every hub member the integration can place and applies the role mapping. It
// never removes anyone (a member added in Moharrik alone stays); removal is member.removed.
func (s *Service) syncMembers(ctx context.Context, workspaceID string, members []circlexo.Member) error {
	var first error
	for _, m := range members {
		id, ok, err := s.resolveMember(ctx, m, true)
		if err == nil && ok {
			err = s.setMember(ctx, workspaceID, id, m.Role)
		}
		if err != nil {
			s.Log.Error("circlexo: member sync", "workspace", workspaceID, "err", err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

func (s *Service) lockMembers(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "circlexo:members:"+workspaceID)
	return err
}

func otherOwners(ctx context.Context, tx *sql.Tx, workspaceID, userID string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'owner' AND status = 'active' AND user_id <> $2`, workspaceID, userID).Scan(&n)
	return n, err
}

// setMember makes userID an active member of the workspace with the role the hub role maps to,
// following the rules at the top of this file.
func (s *Service) setMember(ctx context.Context, workspaceID, userID, hubRole string) error {
	target := mapRole(hubRole)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := s.lockMembers(ctx, tx, workspaceID); err != nil {
		return err
	}
	var cur, status string
	err = tx.QueryRowContext(ctx, `SELECT role, status FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID).Scan(&cur, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, $3, 'active')`, workspaceID, userID, target); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	role := cur
	switch {
	case target == "owner" || target == "admin":
		role = target
	case cur == "owner" || cur == "admin":
		role = "member" // the hub took it away
	}
	if cur == "owner" && role != "owner" {
		n, err := otherOwners(ctx, tx, workspaceID, userID)
		if err != nil {
			return err
		}
		if n == 0 {
			role = "owner" // never leave a workspace without an owner
		}
	}
	if role != cur || status != "active" {
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_members SET role = $3, status = 'active' WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID, role); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// removeMember suspends a hub member's membership (data stays), except the last owner's.
func (s *Service) removeMember(ctx context.Context, workspaceID, userID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := s.lockMembers(ctx, tx, workspaceID); err != nil {
		return err
	}
	var role string
	err = tx.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 AND status = 'active'`, workspaceID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if role == "owner" {
		n, err := otherOwners(ctx, tx, workspaceID, userID)
		if err != nil {
			return err
		}
		if n == 0 {
			s.Log.Warn("circlexo: not removing the last owner of a workspace", "workspace", workspaceID)
			return nil
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workspace_members SET status = 'suspended' WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID); err != nil {
		return err
	}
	return tx.Commit()
}
