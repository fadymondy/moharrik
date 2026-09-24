package mcp

// Docs for the workspace tools (user_tools.go). See docs.go.

var docsWorkspace = map[string]ToolDoc{
	"whoami": {
		AR:      "من يمثّله هذا الرمز: مساحة العمل والمستخدم والدور والصلاحيات السارية.",
		Example: map[string]any{},
		Result: "workspace: Acme Trading (acme) id=0b6f1c2e-5d7a-4e0b-9a41-3c8e2f1d7a90\n" +
			"user: sara@mail.example id=7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47\n" +
			"role: manager\n" +
			"scopes: members:read, contacts:read, contacts:write, inbox:read, inbox:write",
	},
	"list_members": {
		AR:      "أعضاء مساحة عمل الرمز: معرّف المستخدم والبريد والدور والحالة. معرّف المستخدم (أو البريد) هو ما تأخذه assign_conversation وحقول المالك.",
		Example: map[string]any{},
		Result: `{"workspace":"acme","count":2,"members":[` +
			`{"user_id":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","email":"sara@mail.example","role":"manager","status":"active","joined_at":"2026-08-02T10:14:00Z"},` +
			`{"user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","email":"omar@mail.example","role":"member","status":"active","joined_at":"2026-08-11T08:40:00Z"}]}`,
	},
}
