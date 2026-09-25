package imports

import (
	"context"
	"errors"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
)

/*
The workflow import source (LinkedIn/Facebook/Instagram/X, MH-562) is
registered so every connection kind shows up in the "Import from a
connection" list, but it is honestly not-ready, by design rather than by
gap: internal/channels/workflow.go's own doc comment says these channels
exist precisely BECAUSE the network has no usable API — they are fed only
by a browser workflow's scheduled "Push to inbox" step (PushMessages),
which reads whatever the browser happened to see on its last run. There is
no endpoint to ask any of these four networks "what did this thread say
before the workflow started watching it," so, like Telegram, there is
nothing here to import.

Connections() reflects that by always reporting Ready: false with a Note
explaining why, and Preview/Import fail cleanly with that same message.
Summary() still works through genericSummary — but note that ChatSummary
(internal/imports/imports.go) dispatches by a conversation's own channel
column, which for these channels is the network name itself
(PushMessages keys delivery as deliver(..., c.Network, ...)) — "linkedin",
"facebook", "instagram", "x" — never the literal "workflow", so this
provider's Summary is never actually reached by that dispatch; ChatSummary
already falls back to genericSummary for any channel with no registered
provider, so a workflow-channel conversation's summary works correctly
without this provider being reachable for it. Kind() and Summary() are kept
here anyway so the type satisfies Provider and Connections()/Preview()/
Import() are reachable the same way Telegram's are.
*/

const KindWorkflow = "workflow"

const workflowNotReadyNote = "LinkedIn, Facebook, Instagram and X messages reach this inbox only through a browser workflow's scheduled \"Push to inbox\" step — these networks have no API this app can query for older history, so there is nothing here to import. A thread starts itself the moment the workflow next pushes a message from it."

var errWorkflowNoHistory = errors.New(workflowNotReadyNote)

// WorkflowProvider is a deliberately not-ready Provider — see the package
// doc comment above for why.
type WorkflowProvider struct{}

func (WorkflowProvider) Kind() string { return KindWorkflow }

func (WorkflowProvider) Connections(ctx context.Context, a *app.App, wsID string) ([]ImportConnection, error) {
	list, err := channels.ListWorkflowChannels(ctx, a.SQLDB, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]ImportConnection, 0, len(list))
	for _, c := range list {
		label := c.Name
		if c.Network != "" {
			label = c.Name + " (" + c.Network + ")"
		}
		out = append(out, ImportConnection{ID: c.ID, Label: label, Ready: false, Note: workflowNotReadyNote})
	}
	return out, nil
}

func (WorkflowProvider) Preview(ctx context.Context, a *app.App, wsID, connID string, f Filters) ([]Item, error) {
	return nil, errWorkflowNoHistory
}

func (WorkflowProvider) Import(ctx context.Context, a *app.App, wsID string, job *Job, req ImportRequest, progress func(int)) error {
	return errWorkflowNoHistory
}

func (WorkflowProvider) Summary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error) {
	return genericSummary(ctx, a, wsID, conversationID, KindWorkflow)
}

var _ Provider = WorkflowProvider{}
