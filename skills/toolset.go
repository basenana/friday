package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/tools"
)

// NewSkillTools creates the skill management tools
func NewSkillTools(registry *Registry) []*tools.Tool {
	return []*tools.Tool{
		newListSkillsTool(registry),
		newLoadSkillTool(registry),
		newListSkillFilesTool(registry),
		newReadSkillFileTool(registry),
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

// newListSkillFilesTool creates the list_skill_files tool.
func newListSkillFilesTool(registry *Registry) *tools.Tool {
	return tools.NewTool("list_skill_files",
		tools.WithDescription(`List files and directories within a skill's directory.
Use this to explore what's inside a skill - sub-skills, scripts, references, examples, etc.
Leave path empty to list the root directory, or specify a sub-path to browse deeper.`),
		tools.WithString("skill_name",
			tools.Required(),
			tools.Description("The name of the skill to explore"),
		),
		tools.WithString("path",
			tools.Description("Sub-path within the skill directory (empty for root)"),
		),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			skillName, _ := req.Arguments["skill_name"].(string)
			if skillName == "" {
				return tools.NewToolResultError("skill_name parameter is required"), nil
			}
			subPath, _ := req.Arguments["path"].(string)

			entries, err := registry.ListFiles(skillName, subPath)
			if err != nil {
				return tools.NewToolResultError(fmt.Sprintf("Failed to list files: %v", err)), nil
			}

			if len(entries) == 0 {
				return tools.NewToolResultText("Directory is empty."), nil
			}

			var b strings.Builder
			prefix := skillName
			if subPath != "" {
				prefix += "/" + subPath
			}
			b.WriteString(fmt.Sprintf("Contents of %s (%d items):\n\n", prefix, len(entries)))
			for _, e := range entries {
				if e.IsDir() {
					b.WriteString(fmt.Sprintf("[DIR]  %s/\n", e.Name()))
				} else {
					info, _ := e.Info()
					if info != nil {
						b.WriteString(fmt.Sprintf("[FILE] %s (%d bytes)\n", e.Name(), info.Size()))
					} else {
						b.WriteString(fmt.Sprintf("[FILE] %s\n", e.Name()))
					}
				}
			}
			b.WriteString("\nUse read_skill_file(skill_name, path) to read any file.")
			return tools.NewToolResultText(b.String()), nil
		}),
	)
}

// newReadSkillFileTool creates the read_skill_file tool.
func newReadSkillFileTool(registry *Registry) *tools.Tool {
	return tools.NewTool("read_skill_file",
		tools.WithDescription(`Read any file within a skill's directory tree.
Use list_skill_files first to discover available files, then read them with this tool.`),
		tools.WithString("skill_name",
			tools.Required(),
			tools.Description("The name of the skill"),
		),
		tools.WithString("path",
			tools.Required(),
			tools.Description("The file path relative to the skill root (e.g., 'plotly/SKILL.md' or 'references/api.md')"),
		),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			skillName, _ := req.Arguments["skill_name"].(string)
			filePath, _ := req.Arguments["path"].(string)
			if skillName == "" {
				return tools.NewToolResultError("skill_name parameter is required: the name of the skill whose file to read"), nil
			}
			if filePath == "" {
				return tools.NewToolResultError("path parameter is required: the file path relative to the skill's base path"), nil
			}

			content, err := registry.ReadFile(skillName, filePath)
			if err != nil {
				return tools.NewToolResultError(fmt.Sprintf("Failed to read file: %v", err)), nil
			}

			return tools.NewToolResultText(string(content)), nil
		}),
	)
}
