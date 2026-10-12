package opportunities

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// AgentProviderOut is the Opportunities page's "who does the research" panel.
type AgentProviderOut struct {
	Provider research.Provider    `json:"provider"`
	Tasks    []research.AgentTask `json:"tasks" doc:"The latest tasks for an agent, newest first."`
	Prompt   string               `json:"prompt" doc:"What to paste into Claude, Codex or any MCP client to run the finder."`
	MCPURL   string               `json:"mcp_url" doc:"This deployment's MCP endpoint."`
	Tools    []string             `json:"tools" doc:"The MCP tools the agent uses, in order."`
}

func agentScope(ctx context.Context, perm string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, perm) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

// RegisterAgent mounts the agent-provider endpoints. `a` and `engine` may be nil (OpenAPI export).
func RegisterAgent(api huma.API, a *app.App, engine *research.Engine) {
	read := func(ctx context.Context, s *tenant.Scope) (*body[AgentProviderOut], error) {
		p, err := engine.GetProvider(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the research provider")
		}
		tasks, err := research.ListAgentTasks(ctx, a.SQLDB, s.WorkspaceID, 10)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the agent tasks")
		}
		return &body[AgentProviderOut]{AgentProviderOut{Provider: *p, Tasks: tasks, Prompt: AgentPrompt(s.WorkspaceName), MCPURL: MCPURL(),
			Tools: AgentTools}}, nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "w-agent-provider", Method: http.MethodGet, Path: "/api/w/agent-provider",
		Summary: "Who does this workspace's research: the server, or your AI assistant over MCP — with the tasks waiting and the prompt to paste",
		Tags:    []string{"Opportunities"},
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[AgentProviderOut], error) {
		s, err := agentScope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		return read(ctx, s)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-agent-provider-save", Method: http.MethodPut, Path: "/api/w/agent-provider",
		Summary: "Choose who does the research: auto (the server when search is configured, else your AI assistant), server, or agent",
		Tags:    []string{"Opportunities"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Mode string `json:"mode" enum:"auto,server,agent"`
		}
	}) (*body[AgentProviderOut], error) {
		s, err := agentScope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		if _, err := engine.SetProvider(ctx, a.SQLDB, s.WorkspaceID, in.Body.Mode); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return read(ctx, s)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-summarise-agent", Method: http.MethodPost, Path: "/api/w/research/{id}/summarise-via-agent",
		Summary: "Hand a finished, unsummarised run to your AI assistant: queues a summary task it takes over MCP (agent_tasks)",
		Tags:    []string{"Research"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id" maxLength:"64"`
	}) (*body[research.AgentTask], error) {
		s, err := agentScope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		t, err := research.QueueSummary(ctx, a.SQLDB, s.WorkspaceID, in.ID, s.UserID)
		switch {
		case errors.Is(err, research.ErrNoRun):
			return nil, huma.Error404NotFound("no such research run")
		case errors.Is(err, research.ErrNotSummarisable):
			return nil, huma.Error409Conflict(err.Error())
		case err != nil:
			return nil, huma.Error500InternalServerError("could not queue the summary")
		}
		return &body[research.AgentTask]{*t}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-agent-task-cancel", Method: http.MethodPost, Path: "/api/w/agent-tasks/{id}/cancel",
		Summary: "Withdraw a task an agent has not finished", Tags: []string{"Opportunities"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id" maxLength:"64"`
	}) (*body[AgentProviderOut], error) {
		s, err := agentScope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		if err := research.CancelAgentTask(ctx, a.SQLDB, s.WorkspaceID, in.ID); err != nil {
			if errors.Is(err, research.ErrTaskClosed) {
				return nil, huma.Error409Conflict("that task is not waiting or running")
			}
			return nil, huma.Error500InternalServerError("could not cancel the task")
		}
		return read(ctx, s)
	})
}
