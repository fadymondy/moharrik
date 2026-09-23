// Package mcp exposes two Model Context Protocol servers:
//
//	POST /api/mcp        USER MCP  — tokens belong to a user in a workspace
//	POST /api/admin/mcp  ADMIN MCP — platform administration, admin-only tokens
//
// The transport is ported from fadymondy.com-v2's internal/mcp/server.go
// (streamable HTTP, one POST endpoint, JSON-RPC 2.0, SHA-256-hashed bearer
// tokens, best-effort last_used_at, in-band isError), with its opt-in scope
// weakness removed (FM-22): there a tool with an empty Scope was callable by
// any token. Here every tool MUST declare a scope from a closed list
// (scopes_test.go fails the build otherwise), and dispatch denies a tool whose
// scope is empty rather than allowing it — default-deny twice over.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/togo-framework/ratelimit"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/audit"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// protocolVersion is the default MCP revision. Newer clients that ask for a
// revision in supportedVersions get it echoed back (the plan: keep 2024-11-05,
// accept 2025-06-18). This server only speaks request/response tools, which
// did not change shape between them.
const protocolVersion = "2024-11-05"

var supportedVersions = map[string]bool{"2024-11-05": true, "2025-03-26": true, "2025-06-18": true}

// Tool is one callable exposed over MCP.
type Tool struct {
	Name        string
	Description string
	// Scope is REQUIRED. A token must carry it (after narrowing to its user's
	// current role) to call the tool. Empty is a bug and is denied at dispatch.
	Scope       string
	InputSchema map[string]any
	// Run returns the text handed back to the model. ctx carries the caller:
	// tenant.Scope for the user server, adminCaller for the admin server.
	Run func(ctx context.Context, a *app.App, args json.RawMessage) (string, error)
}

// server is one MCP endpoint: a tool registry plus how its tokens authenticate.
type server struct {
	name  string
	tools []Tool
	// auth resolves the bearer token to a context carrying the caller and the
	// scopes in effect. ok=false => 401.
	auth func(r *http.Request, a *app.App, token string) (ctx context.Context, scopes []string, ok bool)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Mount registers both MCP endpoints on the kernel router, rate-limited per
// token when the ratelimit plugin is installed.
func Mount(k *togo.Kernel, a *app.App, researchEngine *research.Engine) {
	user := &server{name: "moharrik-mcp", tools: UserTools(researchEngine), auth: authUser}
	admin := &server{name: "moharrik-admin-mcp", tools: AdminTools(), auth: authAdmin}

	wrap := func(h http.Handler) http.Handler { return h }
	if rl, ok := ratelimit.FromKernel(k); ok {
		wrap = rl.Middleware(ratelimit.Rate("mcp", 120, time.Minute), func(r *http.Request) string {
			// Per token (hashed), falling back to the client address.
			if t := bearer(r); t != "" {
				return "mcp:" + HashToken(t)[:16]
			}
			return "mcp-ip:" + r.RemoteAddr
		})
	}
	k.Router.Handle("/api/mcp", wrap(user.handler(a)))
	k.Router.Handle("/api/admin/mcp", wrap(admin.handler(a)))
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

func (s *server) handler(a *app.App) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeRPC(w, http.StatusMethodNotAllowed, rpcResponse{JSONRPC: "2.0",
				Error: &rpcError{Code: -32600, Message: "use POST"}})
			return
		}
		ctx, scopes, ok := s.auth(req, a, bearer(req))
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+s.name+`"`)
			writeRPC(w, http.StatusUnauthorized, rpcResponse{JSONRPC: "2.0",
				Error: &rpcError{Code: -32001, Message: "invalid or missing MCP token"}})
			return
		}

		var rpc rpcRequest
		if json.NewDecoder(http.MaxBytesReader(w, req.Body, 4<<20)).Decode(&rpc) != nil {
			writeRPC(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0",
				Error: &rpcError{Code: -32700, Message: "parse error"}})
			return
		}
		res := s.dispatch(ctx, a, scopes, rpc)
		if len(rpc.ID) == 0 { // a notification gets no body, per JSON-RPC
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, http.StatusOK, res)
	})
}

func (s *server) dispatch(ctx context.Context, a *app.App, scopes []string, rpc rpcRequest) rpcResponse {
	reply := func(result any) rpcResponse { return rpcResponse{JSONRPC: "2.0", ID: rpc.ID, Result: result} }
	fail := func(code int, msg string) rpcResponse {
		return rpcResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &rpcError{Code: code, Message: msg}}
	}
	inBand := func(text string, isErr bool) rpcResponse {
		out := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
		if isErr {
			out["isError"] = true
		}
		return reply(out)
	}

	switch rpc.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(rpc.Params, &p)
		version := protocolVersion
		if supportedVersions[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		return reply(map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.name, "version": "0.1.0"},
		})
	case "ping", "notifications/initialized":
		return reply(map[string]any{})
	case "tools/list":
		return reply(map[string]any{"tools": s.schemas()})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(rpc.Params, &params) != nil {
			return fail(-32602, "invalid params")
		}
		tool, ok := s.tool(params.Name)
		if !ok {
			return fail(-32602, "unknown tool: "+params.Name)
		}
		// The audit log (MH-392): the call is named on the request, so every
		// row it changes is recorded as this tool; a write that changed no
		// audited row — refused, failed, or a send — gets a request row.
		tenant.RequestFrom(ctx).SetOperation("mcp:" + tool.Name)
		// Default-deny: an undeclared scope is never satisfied (FM-22 fixed).
		if tool.Scope == "" || !hasScope(scopes, tool.Scope) {
			auditCall(ctx, a, tool, params.Arguments, http.StatusForbidden)
			return inBand(fmt.Sprintf(
				"%s needs the %q scope, which this token does not carry (it has: %s). "+
					"Mint a token with that scope to allow this.",
				tool.Name, tool.Scope, strings.Join(nonEmpty(scopes), ", ")), true)
		}
		text, err := tool.Run(ctx, a, params.Arguments)
		if err != nil {
			auditCall(ctx, a, tool, params.Arguments, http.StatusBadRequest)
			return inBand(err.Error(), true)
		}
		auditCall(ctx, a, tool, params.Arguments, http.StatusOK)
		return inBand(text, false)
	default:
		return fail(-32601, "method not found: "+rpc.Method)
	}
}

func (s *server) tool(name string) (Tool, bool) {
	for _, t := range s.tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

func (s *server) schemas() []map[string]any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description + " (scope: " + t.Scope + ")",
			"inputSchema": t.InputSchema,
		})
	}
	return out
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

func nonEmpty(s []string) []string {
	if len(s) == 0 {
		return []string{"none"}
	}
	return s
}

func writeRPC(w http.ResponseWriter, status int, res rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}

// ---- small arg helpers (fadymondy.com-v2 tools.go) ----------------------

func decodeArgs(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func intArg(m map[string]any, k string, def int) int {
	if v, ok := m[k].(float64); ok && v > 0 {
		return int(v)
	}
	return def
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func schema(required []string, props map[string]any) map[string]any {
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func prop(typ, desc string) map[string]any {
	p := map[string]any{"type": typ}
	if desc != "" {
		p["description"] = desc
	}
	return p
}

// IsWriteTool reports whether a tool writes: its scope is anything but a
// ":read" scope. Every write tool's call is audited (MH-392); a new tool is
// audited unless its scope says it only reads.
func IsWriteTool(t Tool) bool {
	return !strings.HasSuffix(t.Scope, ":read")
}

// auditCall writes the request row for a write tool's call when it changed no
// audited row (audit.RecordRequest decides).
func auditCall(ctx context.Context, a *app.App, t Tool, raw json.RawMessage, status int) {
	if !IsWriteTool(t) {
		return
	}
	args := decodeArgs(raw)
	entityID := str(args, "id")
	entityType := ""
	if i := strings.IndexByte(t.Name, '_'); i >= 0 {
		entityType = t.Name[i+1:]
	}
	if entityID == "" {
		for k, v := range args {
			if s, ok := v.(string); ok && strings.HasSuffix(k, "_id") && s != "" {
				entityID, entityType = s, strings.TrimSuffix(k, "_id")
				break
			}
		}
	}
	audit.RecordRequest(ctx, a, audit.Fallback{
		Operation: "mcp:" + t.Name, Status: status, EntityType: entityType, EntityID: entityID,
	})
}
