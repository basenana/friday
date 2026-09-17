package agents

import (
	"context"
	"fmt"
	"strings"

	coreagents "github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/subagents"
)

const RouteMetadataKey = "friday.agent_name"

// Router selects a named agent for one request. Selection is request-scoped;
// requests without RouteMetadataKey always use the primary agent.
type Router struct {
	primary  coreagents.Agent
	provider subagents.AgentProvider
}

func NewRouter(primary coreagents.Agent, experts []subagents.ExpertAgent) *Router {
	return NewDynamicRouter(primary, staticExperts(experts))
}

type staticExperts []subagents.ExpertAgent

func (s staticExperts) List() []subagents.ExpertAgent {
	return append([]subagents.ExpertAgent(nil), s...)
}

// NewDynamicRouter resolves named agents at request time so an existing root
// session observes hot-loaded additions, updates, and removals.
func NewDynamicRouter(primary coreagents.Agent, provider subagents.AgentProvider) *Router {
	return &Router{primary: primary, provider: provider}
}

func (r *Router) Chat(ctx context.Context, req *api.Request) *api.Response {
	name := ""
	if req != nil && req.Metadata != nil {
		name = strings.ToLower(strings.TrimSpace(req.Metadata[RouteMetadataKey]))
	}
	if name == "" {
		return r.primary.Chat(ctx, req)
	}
	var agent coreagents.Agent
	if r.provider != nil {
		for _, candidate := range r.provider.List() {
			if strings.EqualFold(candidate.Name, name) {
				agent = candidate.Agent
				break
			}
		}
	}
	if agent == nil {
		resp := api.NewResponse()
		resp.Fail(fmt.Errorf("agent not found: %s", name))
		resp.Close()
		return resp
	}
	return agent.Chat(ctx, req)
}
