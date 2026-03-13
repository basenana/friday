package skills

import (
	"html"
	"strings"
)

// SKILL_SYSTEM_PROMPT is appended to system prompt to explain skills usage
const SKILL_SYSTEM_PROMPT = `
## Skills

Skills are specialized capabilities that provide expert knowledge for specific tasks.

### How to use skills:
1. First, use the list_skills tool to see available skills
2. When you need expert help, use load_skill(name) to load the skill's instructions
3. The skill instructions will be added to our conversation
4. Follow the instructions to accomplish the task

### Important notes:
- Skills are loaded on-demand to save context space
- After loading a skill, its instructions remain available for the conversation
- You can load multiple skills if needed for complex tasks
`

// FormatSkillsAsXML formats skills list as XML for system prompt injection
func FormatSkillsAsXML(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<available_skills>\n")
	for _, skill := range skills {
		sb.WriteString("  <skill>\n")
		sb.WriteString("    <name>" + escapeXML(skill.Name) + "</name>\n")
		sb.WriteString("    <description>" + escapeXML(skill.Description) + "</description>\n")
		sb.WriteString("  </skill>\n")
	}
	sb.WriteString("</available_skills>")
	return sb.String()
}

// escapeXML escapes special characters for safe XML content
func escapeXML(s string) string {
	return html.EscapeString(s)
}
