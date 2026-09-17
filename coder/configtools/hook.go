package configtools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

const (
	AgentToolName = "agent_config"
	MCPToolName   = "mcp_config"
)

type Hook struct {
	store Store
}

func NewHook(store Store) *Hook { return &Hook{store: store} }

func (h *Hook) BeforeAgent(_ context.Context, _ *session.Session, req session.AgentRequest) error {
	if h == nil || h.store == nil || req == nil {
		return nil
	}
	existing := make(map[string]struct{})
	for _, tool := range req.GetTools() {
		if tool != nil {
			existing[tool.Name] = struct{}{}
		}
	}
	for _, tool := range []*tools.Tool{newAgentTool(h.store), newMCPTool(h.store)} {
		if _, ok := existing[tool.Name]; !ok {
			req.AppendTools(tool)
		}
	}
	return nil
}

func newAgentTool(store Store) *tools.Tool {
	return tools.NewTool(AgentToolName,
		tools.WithDescription("List, inspect, create, update, or delete declarative subagent configurations. Changes become available through hot reload on the next agent turn."),
		tools.WithString("action", tools.Required(), tools.Enum("list", "get", "create", "update", "delete"), tools.Description("Configuration operation to perform.")),
		tools.WithString("name", tools.Description("Lowercase agent name. Required except for list.")),
		tools.WithString("description", tools.Description("Short routing description. Used by create/update.")),
		tools.WithString("instructions", tools.Description("Complete agent system instructions. Required by create; optional replacement for update.")),
		tools.WithString("model", tools.Description("Configured model name; empty inherits the session model.")),
		tools.WithString("effort", tools.Enum("", "default", "none", "low", "medium", "high", "xhigh", "max"), tools.Description("Reasoning effort override.")),
		tools.WithInteger("max_loop_times", tools.Min(1), tools.Description("Positive maximum agent loop count.")),
		tools.WithBoolean("confirm_delete", tools.Description("Must be true for delete.")),
		tools.WithToolHandler(func(_ context.Context, req *tools.Request) (*tools.Result, error) {
			action, _ := stringField(req.Arguments, "action")
			name, _ := stringField(req.Arguments, "name")
			switch action {
			case "list":
				specs, err := store.ListAgents()
				if err != nil {
					return configError(err), nil
				}
				items := make([]map[string]any, 0, len(specs))
				for _, spec := range specs {
					items = append(items, map[string]any{"name": spec.Name, "description": spec.Description, "model": spec.Model, "effort": spec.Effort, "source": spec.SourcePath})
				}
				return jsonResult(items), nil
			case "get":
				if name == "" {
					return configError(fmt.Errorf("name is required for get")), nil
				}
				spec, err := store.GetAgent(name)
				if err != nil {
					return configError(err), nil
				}
				return jsonResult(map[string]any{"name": spec.Name, "description": spec.Description, "instructions": spec.SystemPrompt, "model": spec.Model, "effort": spec.Effort, "max_loop_times": spec.MaxLoopTimes, "source": spec.SourcePath}), nil
			case "create":
				input := AgentInput{Name: name}
				applyAgentFields(&input, req.Arguments)
				spec, err := store.CreateAgent(input)
				if err != nil {
					return configError(err), nil
				}
				return jsonResult(map[string]any{"created": spec.Name, "source": spec.SourcePath, "effective_next_turn": true}), nil
			case "update":
				if name == "" {
					return configError(fmt.Errorf("name is required for update")), nil
				}
				spec, err := store.UpdateAgent(name, req.Arguments)
				if err != nil {
					return configError(err), nil
				}
				return jsonResult(map[string]any{"updated": spec.Name, "source": spec.SourcePath, "effective_next_turn": true}), nil
			case "delete":
				if name == "" {
					return configError(fmt.Errorf("name is required for delete")), nil
				}
				if confirmed, _ := req.Arguments["confirm_delete"].(bool); !confirmed {
					return configError(fmt.Errorf("confirm_delete=true is required")), nil
				}
				if err := store.DeleteAgent(name); err != nil {
					return configError(err), nil
				}
				result := map[string]any{"deleted": name, "effective_next_turn": true}
				if revealed, err := store.GetAgent(name); err == nil {
					result["revealed_inherited"] = map[string]any{"name": revealed.Name, "source": revealed.SourcePath}
				}
				return jsonResult(result), nil
			default:
				return configError(fmt.Errorf("unsupported action %q", action)), nil
			}
		}),
	)
}

func newMCPTool(store Store) *tools.Tool {
	stringArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	stringMap := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
	configProperties := map[string]any{
		"type":         map[string]any{"type": "string", "enum": []string{"stdio", "sse", "streamable-http"}},
		"command":      map[string]any{"type": "string"},
		"args":         stringArray,
		"env":          stringMap,
		"cwd":          map[string]any{"type": "string"},
		"url":          map[string]any{"type": "string"},
		"headers":      stringMap,
		"disabled":     map[string]any{"type": "boolean"},
		"includeTools": stringArray,
		"excludeTools": stringArray,
	}
	return tools.NewTool(MCPToolName,
		tools.WithDescription("List, inspect, create, update, or delete MCP server configurations. Project changes still require the normal MCP trust flow and become available on the next agent turn."),
		tools.WithString("action", tools.Required(), tools.Enum("list", "get", "create", "update", "delete"), tools.Description("Configuration operation to perform.")),
		tools.WithString("name", tools.Description("MCP server name. Required except for list.")),
		tools.WithObject("config", tools.Properties(configProperties), tools.AdditionalProperties(false), tools.Description("Server fields for create or a partial field patch for update.")),
		tools.WithBoolean("confirm_delete", tools.Description("Must be true for delete.")),
		tools.WithExample(map[string]any{"action": "create", "name": "example", "config": map[string]any{"type": "streamable-http", "url": "https://example.invalid/mcp"}}),
		tools.WithToolHandler(func(_ context.Context, req *tools.Request) (*tools.Result, error) {
			action, _ := stringField(req.Arguments, "action")
			name, _ := stringField(req.Arguments, "name")
			fields, _ := req.Arguments["config"].(map[string]any)
			switch action {
			case "list":
				configs, err := store.ListMCP()
				if err != nil {
					return configError(err), nil
				}
				items := make([]map[string]any, 0, len(configs))
				for _, config := range configs {
					items = append(items, RedactedMCP(config))
				}
				return jsonResult(items), nil
			case "get":
				if name == "" {
					return configError(fmt.Errorf("name is required for get")), nil
				}
				config, err := store.GetMCP(name)
				if err != nil {
					return configError(err), nil
				}
				return jsonResult(RedactedMCP(config)), nil
			case "create":
				if name == "" || len(fields) == 0 {
					return configError(fmt.Errorf("name and config are required for create")), nil
				}
				config, err := store.CreateMCP(name, fields)
				if err != nil {
					return configError(err), nil
				}
				return jsonResult(map[string]any{"created": config.Name, "source": config.Source, "requires_trust": config.Project, "effective_next_turn": true}), nil
			case "update":
				if name == "" || len(fields) == 0 {
					return configError(fmt.Errorf("name and non-empty config are required for update")), nil
				}
				config, err := store.UpdateMCP(name, fields)
				if err != nil {
					return configError(err), nil
				}
				return jsonResult(map[string]any{"updated": config.Name, "source": config.Source, "requires_trust": config.Project, "effective_next_turn": true}), nil
			case "delete":
				if name == "" {
					return configError(fmt.Errorf("name is required for delete")), nil
				}
				if confirmed, _ := req.Arguments["confirm_delete"].(bool); !confirmed {
					return configError(fmt.Errorf("confirm_delete=true is required")), nil
				}
				if err := store.DeleteMCP(name); err != nil {
					return configError(err), nil
				}
				result := map[string]any{"deleted": name, "effective_next_turn": true}
				if revealed, err := store.GetMCP(name); err == nil {
					result["revealed_inherited"] = map[string]any{"name": revealed.Name, "source": revealed.Source}
				}
				return jsonResult(result), nil
			default:
				return configError(fmt.Errorf("unsupported action %q", action)), nil
			}
		}),
	)
}

func jsonResult(value any) *tools.Result {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return tools.NewToolResultError("failed to encode configuration result")
	}
	return tools.NewToolResultText(string(data))
}

func configError(err error) *tools.Result {
	if err == nil {
		return tools.NewToolResultError("configuration operation failed")
	}
	return tools.NewToolResultActionableError(err.Error(), "correct the operation or fields and retry")
}

var _ session.BeforeAgentHook = (*Hook)(nil)
