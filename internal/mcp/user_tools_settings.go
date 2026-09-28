package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/audit"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

/*
SettingsTools (MH-680): the workspace settings a member can change on the page but
a token could not — "when i try to create a token there is a lot of missed
permistion to push for!" (Fady). Each scope is one authz line:

	workspace:update     authz.WorkspaceUpdate     rename, the picture
	members:manage       authz.MembersManage       invite, revoke, change a role, remove
	crm:manage           authz.PipelineManage      the pipeline's stages (and the ICP, MH-395)
	mail:settings        authz.MailSettings        the SMTP services mail goes out through
	integrations:manage  authz.IntegrationsManage  Google connections and what they read
	audit:manage         authz.AuditManage         how long the audit log is kept

Every tool calls the same function its REST route does, which checks the
member's role again, so a token never does more than its member could on the
page. Deleting the workspace stays on the page: it cannot be undone.
*/
func SettingsTools() []Tool {
	return []Tool{
		{
			Name:        "update_workspace",
			Description: "Rename the workspace (1-80 characters). Its address (slug) does not change. Returns the workspace.",
			Scope:       "workspace:update",
			InputSchema: schema([]string{"name"}, map[string]any{"name": prop("string", "The new name")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Name string `json:"name"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if err := workspaces.Rename(ctx, a.SQLDB, s.WorkspaceID, s.Role, in.Name); err != nil {
					return "", err
				}
				return workspaceResult(ctx, a, s.WorkspaceID)
			},
		},
		{
			Name: "set_workspace_picture",
			Description: "Upload or replace the workspace's picture, shown in the workspace list, the switcher and the sidebar. " +
				"PNG, JPEG or WEBP, base64-encoded, up to 2MB. Returns the workspace with its avatar_url.",
			Scope: "workspace:update",
			InputSchema: schema([]string{"data", "content_type"}, map[string]any{
				"data":         prop("string", "The image, base64-encoded (no data: prefix)"),
				"content_type": map[string]any{"type": "string", "enum": []string{"image/png", "image/jpeg", "image/webp"}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Data        string `json:"data"`
					ContentType string `json:"content_type"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				data := in.Data
				if i := strings.Index(data, ";base64,"); strings.HasPrefix(data, "data:") && i > 0 {
					data = data[i+len(";base64,"):]
				}
				img, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
				if err != nil {
					return "", errors.New("data is not valid base64")
				}
				if err := workspaces.SetAvatar(ctx, a.SysDB, s.WorkspaceID, s.Role, in.ContentType, img); err != nil {
					return "", err
				}
				return workspaceResult(ctx, a, s.WorkspaceID)
			},
		},
		{
			Name:        "remove_workspace_picture",
			Description: "Remove the workspace's picture; its initial shows again. Returns the workspace.",
			Scope:       "workspace:update",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if err := workspaces.RemoveAvatar(ctx, a.SysDB, s.WorkspaceID, s.Role); err != nil {
					return "", err
				}
				return workspaceResult(ctx, a, s.WorkspaceID)
			},
		},
		{
			Name: "invite_member",
			Description: "Invite someone to the workspace by email, as admin, manager, member or client — never above this token's " +
				"member's own role. No email is sent: pass accept_url on to them yourself. The link is shown only this once, is " +
				"bound to that email, and expires in 7 days. Refused if they are already a member.",
			Scope: "members:manage",
			InputSchema: schema([]string{"email", "role"}, map[string]any{
				"email": prop("string", "Their email address; they accept with an account on this address"),
				"role":  map[string]any{"type": "string", "enum": []string{"admin", "manager", "member", "client"}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Email string `json:"email"`
					Role  string `json:"role"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				inv, link, err := workspaces.Invite(ctx, a.SQLDB, a.SysDB, s.WorkspaceID, s.Role, s.UserID, in.Email, strings.TrimSpace(in.Role))
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"invitation": inv, "accept_url": publicURL(a, link)})
			},
		},
		{
			Name:        "list_invitations",
			Description: "The workspace's pending invitations: id, email, role, when each expires. Links are not shown again.",
			Scope:       "members:manage",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				list, err := workspaces.Invitations(ctx, a.SQLDB, s.WorkspaceID, s.Role)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"count": len(list), "invitations": list})
			},
		},
		{
			Name:        "revoke_invitation",
			Description: "Revoke a pending invitation by its id (list_invitations); its link stops working.",
			Scope:       "members:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Invitation id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if err := workspaces.RevokeInvitation(ctx, a.SQLDB, s.WorkspaceID, s.Role, id); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"ok": true, "revoked": id})
			},
		},
		{
			Name: "set_member_role",
			Description: "Change a member's role (owner, admin, manager, member, client), by user id or email (list_members). " +
				"Nobody gives a role above their own; only an owner changes an owner or makes someone one; the last owner " +
				"cannot be demoted. Returns the members.",
			Scope: "members:manage",
			InputSchema: schema([]string{"member", "role"}, map[string]any{
				"member": prop("string", "The member's user id or email"),
				"role":   map[string]any{"type": "string", "enum": []string{"owner", "admin", "manager", "member", "client"}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Member string `json:"member"`
					Role   string `json:"role"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				s, uid, err := targetMember(ctx, a, in.Member)
				if err != nil {
					return "", err
				}
				if !authz.Can(s.Role, authz.MembersManage) {
					return "", errors.New("only owners and admins change members' roles")
				}
				if err := workspaces.SetRole(ctx, a.SysDB, s.WorkspaceID, s.Role, uid, strings.TrimSpace(in.Role)); err != nil {
					return "", err
				}
				return membersResult(ctx, a, s.WorkspaceID)
			},
		},
		{
			Name: "remove_member",
			Description: "Remove a member from the workspace, by user id or email (list_members). Their tokens and connected apps " +
				"for this workspace are revoked with them. Only an owner removes an owner, and the last owner stays. A token " +
				"cannot remove its own member — leaving is done on the page. Returns the members.",
			Scope:       "members:manage",
			InputSchema: schema([]string{"member"}, map[string]any{"member": prop("string", "The member's user id or email")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Member string `json:"member"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				s, uid, err := targetMember(ctx, a, in.Member)
				if err != nil {
					return "", err
				}
				if uid == s.UserID {
					return "", errors.New("a token cannot remove its own member; leave the workspace from the Workspaces page")
				}
				if !authz.Can(s.Role, authz.MembersManage) {
					return "", errors.New("only owners and admins remove members")
				}
				if err := workspaces.RemoveMember(ctx, a.SysDB, s.WorkspaceID, s.Role, uid, false); err != nil {
					return "", err
				}
				return membersResult(ctx, a, s.WorkspaceID)
			},
		},
		{
			Name: "set_pipeline_stages",
			Description: "Replace the deal pipeline: rename, reorder, add and remove stages in one call. Give every stage in its new " +
				"order — an existing one with its id (list_deals returns the stages), a new one without. A stage left out is removed, " +
				"which is refused while it holds deals. kind is open, won or lost; at least one stage stays open, and deals in a " +
				"stage that changes kind open or close with it. 1 to 20 stages. Returns the pipeline.",
			Scope: "crm:manage",
			InputSchema: schema([]string{"stages"}, map[string]any{
				"stages": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{
					"type": "object", "required": []string{"name", "kind"}, "properties": map[string]any{
						"id":   prop("string", "An existing stage's id; omit to add a stage"),
						"name": prop("string", "1-40 characters"),
						"kind": map[string]any{"type": "string", "enum": []string{"open", "won", "lost"}},
					}}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Stages []crm.StageIn `json:"stages"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				st, err := crm.SaveStages(ctx, a.SQLDB, in.Stages)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"stages": st})
			},
		},
		{
			Name: "smtp_services",
			Description: "The SMTP services the workspace's mail goes out through, in full for the admins who manage them (list_smtp_services " +
				"gives campaign writers just the names): id, host, port, sender, TLS, which is the default " +
				"(transactional mail) and the campaign default, and the last test's outcome. Passwords are never returned; " +
				"has_password says whether one is stored.",
			Scope:       "mail:settings",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := mailScope(ctx)
				if err != nil {
					return "", err
				}
				list, err := marketing.ListSMTPServices(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"count": len(list), "services": list})
			},
		},
		{
			Name: "create_smtp_service",
			Description: "Add an SMTP service. The first one becomes the default and the campaign default. The password is sealed " +
				"at rest and never returned by any tool. Test it from Settings → Email before relying on it.",
			Scope:       "mail:settings",
			InputSchema: schema([]string{"name", "host", "port", "from_email"}, smtpProps()),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := mailScope(ctx)
				if err != nil {
					return "", err
				}
				in, err := bindSMTP(raw)
				if err != nil {
					return "", err
				}
				svc, err := marketing.CreateSMTPService(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"service": svc})
			},
		},
		{
			Name: "update_smtp_service",
			Description: "Change an SMTP service by its id (smtp_services). Give the whole service; an omitted password keeps " +
				"the stored one, an empty one clears it.",
			Scope:       "mail:settings",
			InputSchema: schema([]string{"id", "name", "host", "port", "from_email"}, withID(smtpProps(), "SMTP service id")),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := mailScope(ctx)
				if err != nil {
					return "", err
				}
				var ref struct {
					ID string `json:"id"`
				}
				if err := bind(raw, &ref); err != nil {
					return "", err
				}
				id, err := required("id", ref.ID)
				if err != nil {
					return "", err
				}
				in, err := bindSMTP(raw)
				if err != nil {
					return "", err
				}
				svc, err := marketing.UpdateSMTPService(ctx, a.SQLDB, s.WorkspaceID, id, in)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"service": svc})
			},
		},
		{
			Name: "delete_smtp_service",
			Description: "Delete an SMTP service by its id. The default cannot be deleted while others exist — make another the " +
				"default first (set_default_smtp_service).",
			Scope:       "mail:settings",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "SMTP service id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := mailScope(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if err := marketing.DeleteSMTPService(ctx, a.SQLDB, s.WorkspaceID, id); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"ok": true, "deleted": id})
			},
		},
		{
			Name: "set_default_smtp_service",
			Description: "Make an SMTP service the default for transactional mail (confirmations, inbox replies), or with " +
				"campaign=true the one campaigns use when they name none. Returns the services.",
			Scope: "mail:settings",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":       prop("string", "SMTP service id"),
				"campaign": prop("boolean", "true: the campaign default; false or omitted: the transactional default"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := mailScope(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID       string `json:"id"`
					Campaign bool   `json:"campaign"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if err := marketing.SetSMTPDefault(ctx, a.SQLDB, s.WorkspaceID, id, in.Campaign); err != nil {
					return "", err
				}
				list, err := marketing.ListSMTPServices(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"count": len(list), "services": list})
			},
		},
		{
			Name: "list_google_connections",
			Description: "The Google accounts connected to the workspace, what each was granted (GA4, Search Console, YouTube), " +
				"who connected it, and whether it needs reconnecting. Connecting a new account is done on the page: Google " +
				"asks the person themselves.",
			Scope:       "integrations:manage",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := integrationsScope(ctx)
				if err != nil {
					return "", err
				}
				list, err := google.List(ctx, a, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"count": len(list), "connections": list})
			},
		},
		{
			Name: "disconnect_google_connection",
			Description: "Disconnect a Google account by its id (list_google_connections): its grant is revoked at Google unless " +
				"the same account is still connected elsewhere in Moharrik (shared=true), and its properties, sites and " +
				"channels stop syncing here.",
			Scope:       "integrations:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Connection id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := integrationsScope(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				out, err := google.Disconnect(ctx, a, s.WorkspaceID, id)
				if err != nil {
					return "", err
				}
				return jsonResult(out)
			},
		},
		{
			Name: "list_google_resources",
			Description: "Every GA4 property, Search Console site and YouTube channel the connected accounts can see, switched on " +
				"or not, with the account it is read through. google_resources lists only the enabled ones. rediscover=true " +
				"asks Google again first.",
			Scope: "integrations:manage",
			InputSchema: schema(nil, map[string]any{
				"rediscover": prop("boolean", "Ask Google again what every connected account can see"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := integrationsScope(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					Rediscover bool `json:"rediscover"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				if in.Rediscover {
					if err := google.DiscoverWorkspace(ctx, a, s.WorkspaceID, false); err != nil {
						return "", fmt.Errorf("could not ask Google: %w", err)
					}
				}
				list, err := google.ListResources(ctx, a, s.WorkspaceID, google.Filter{})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"count": len(list), "resources": list})
			},
		},
		{
			Name: "set_google_resource",
			Description: "Switch a GA4 property, Search Console site or YouTube channel on or off for this workspace, by its id " +
				"(list_google_resources). Switching one on starts syncing it.",
			Scope: "integrations:manage",
			InputSchema: schema([]string{"id", "enabled"}, map[string]any{
				"id":      prop("string", "Resource id"),
				"enabled": prop("boolean", ""),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := integrationsScope(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID      string `json:"id"`
					Enabled *bool  `json:"enabled"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if in.Enabled == nil {
					return "", errors.New("enabled is required")
				}
				r, err := google.SetEnabled(ctx, a, s.WorkspaceID, id, *in.Enabled)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"resource": r})
			},
		},
		{
			Name:        "set_audit_retention",
			Description: "How long the audit log is kept, in days (30 to 3650). Older entries are deleted. The change is itself logged.",
			Scope:       "audit:manage",
			InputSchema: schema([]string{"days"}, map[string]any{"days": prop("integer", "30 to 3650")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Days int `json:"days"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				days, err := audit.SetRetention(ctx, a.SQLDB, in.Days)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"retention_days": days})
			},
		},
	}
}

func workspaceResult(ctx context.Context, a *app.App, workspaceID string) (string, error) {
	w, err := workspaces.Read(ctx, a.SysDB, workspaceID)
	if err != nil {
		return "", err
	}
	if w.AvatarURL != "" {
		w.AvatarURL = publicURL(a, w.AvatarURL)
	}
	return jsonResult(map[string]any{"workspace": w})
}

func membersResult(ctx context.Context, a *app.App, workspaceID string) (string, error) {
	list, err := workspaces.Members(ctx, a.SysDB, workspaceID)
	if err != nil {
		return "", err
	}
	return jsonResult(map[string]any{"count": len(list), "members": list})
}

// targetMember resolves a member given by user id or email.
func targetMember(ctx context.Context, a *app.App, who string) (s *scopeT, uid string, err error) {
	if strings.TrimSpace(who) == "" {
		return nil, "", errors.New("member is required")
	}
	if s, err = need(ctx); err != nil {
		return nil, "", err
	}
	uid, err = memberID(ctx, a, who)
	return s, uid, err
}

func mailScope(ctx context.Context) (*scopeT, error) {
	s, err := need(ctx)
	if err == nil && !authz.Can(s.Role, authz.MailSettings) {
		err = errors.New("only owners and admins change the mail settings")
	}
	return s, err
}

func integrationsScope(ctx context.Context) (*scopeT, error) {
	s, err := need(ctx)
	if err == nil && !authz.Can(s.Role, authz.IntegrationsManage) {
		err = errors.New("only owners and admins manage integrations")
	}
	return s, err
}

func smtpProps() map[string]any {
	return map[string]any{
		"name":       prop("string", "A name to tell services apart"),
		"host":       prop("string", "SMTP host, e.g. smtp.gmail.com"),
		"port":       prop("integer", "Usually 587 (starttls) or 465 (tls)"),
		"username":   prop("string", ""),
		"password":   prop("string", "Write-only. Omit to keep the stored one; empty clears it"),
		"from_name":  prop("string", "The sender's name"),
		"from_email": prop("string", "The sender's address"),
		"tls_mode":   map[string]any{"type": "string", "enum": []string{"starttls", "tls", "none"}, "description": "Defaults to starttls"},
	}
}

func withID(props map[string]any, desc string) map[string]any {
	props["id"] = prop("string", desc)
	return props
}

func bindSMTP(raw json.RawMessage) (marketing.SMTPServiceInput, error) {
	var in struct {
		Name      string  `json:"name"`
		Host      string  `json:"host"`
		Port      int64   `json:"port"`
		Username  string  `json:"username"`
		Password  *string `json:"password"`
		FromName  string  `json:"from_name"`
		FromEmail string  `json:"from_email"`
		TLSMode   string  `json:"tls_mode"`
	}
	if err := bind(raw, &in); err != nil {
		return marketing.SMTPServiceInput{}, err
	}
	if in.TLSMode == "" {
		in.TLSMode = "starttls"
	}
	return marketing.SMTPServiceInput{Name: in.Name, Host: in.Host, Port: in.Port, Username: in.Username, Password: in.Password,
		FromName: in.FromName, FromEmail: in.FromEmail, TLSMode: in.TLSMode}, nil
}

type scopeT = tenant.Scope

// publicURL makes an app path absolute on the app's own origin. A path with
// no locale is sent to the visitor's language by web/proxy.ts.
func publicURL(_ *app.App, path string) string { return OAuthIssuer() + path }
