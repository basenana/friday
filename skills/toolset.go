package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/basenana/friday/core/tools"
)

// NewSkillTools creates the skill management tools
func NewSkillTools(registry *Registry) []*tools.Tool {
	return []*tools.Tool{
		newListSkillsTool(registry),
		newLoadSkillTool(registry),
		newLoadSkillResourceTool(registry),
	}
}

// newListSkillsTool creates the list_skills tool
func newListSkillsTool(registry *Registry) *tools.Tool {
	return tools.NewTool("list_skills",
		tools.WithDescription(`List all available skills that can be loaded.
Returns skill names and descriptions for discovery.
Use this tool first to see what skills are available.`),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			skills := registry.List()
			if len(skills) == 0 {
				return tools.NewToolResultText("No skills available."), nil
			}

			result := "Available skills:\n\n"
			for _, skill := range skills {
				result += fmt.Sprintf("- %s: %s\n", skill.Name, skill.Description)
			}
			result += "\nUse load_skill(name) to load a skill's instructions."

			return tools.NewToolResultText(result), nil
		}),
	)
}

// newLoadSkillTool creates the load_skill tool
func newLoadSkillTool(registry *Registry) *tools.Tool {
	return tools.NewTool("load_skill",
		tools.WithDescription(`Load and return the complete instructions for a skill.
Use this after discovering skills with list_skills to get the full instructions.
The instructions will be added to the conversation context.`),
		tools.WithString("name",
			tools.Required(),
			tools.Description("The name of the skill to load"),
		),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			name, ok := req.Arguments["name"].(string)
			if !ok {
				return tools.NewToolResultError("name parameter is required"), nil
			}

			skill, err := registry.Get(name)
			if err != nil {
				return tools.NewToolResultError(fmt.Sprintf("Failed to load skill: %v", err)), nil
			}

			result := map[string]interface{}{
				"name":         skill.Name,
				"description":  skill.Description,
				"instructions": skill.Instructions,
			}

			if skill.Frontmatter != nil {
				result["allowed_tools"] = skill.Frontmatter.AllowedTools
			}

			jsonResult, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return tools.NewToolResultError("failed to format skill"), nil
			}

			return tools.NewToolResultText(string(jsonResult)), nil
		}),
	)
}

// newLoadSkillResourceTool creates the load_skill_resource tool
func newLoadSkillResourceTool(registry *Registry) *tools.Tool {
	return tools.NewTool("load_skill_resource",
		tools.WithDescription(`Load a resource file from a skill.
Skills may have reference files, documentation, or other resources.
Use this to access additional context needed for the skill.`),
		tools.WithString("skill_name",
			tools.Required(),
			tools.Description("The name of the skill"),
		),
		tools.WithString("resource_path",
			tools.Required(),
			tools.Description("The relative path to the resource file (e.g., references/api-docs.md)"),
		),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			skillName, ok := req.Arguments["skill_name"].(string)
			if !ok {
				return tools.NewToolResultError("skill_name parameter is required"), nil
			}

			resourcePath, ok := req.Arguments["resource_path"].(string)
			if !ok {
				return tools.NewToolResultError("resource_path parameter is required"), nil
			}

			content, err := registry.LoadResource(skillName, resourcePath)
			if err != nil {
				return tools.NewToolResultError(fmt.Sprintf("Failed to load resource: %v", err)), nil
			}

			return tools.NewToolResultText(string(content)), nil
		}),
	)
}
