package skills

import (
	"bytes"
	"fmt"
	"strings"
)

// SKILL_SYSTEM_PROMPT is appended to system prompt to explain skills usage
const SKILL_SYSTEM_PROMPT = `<skills_system>
You have access to a skills library that provides specialized capabilities and domain knowledge.

{skills_locations}

**Available Skills:**

{skills_list}

**How to Use Skills (Progressive Disclosure):**

Skills follow a **progressive disclosure** pattern - you see their name and description above, but only read full instructions when needed:

1. **Recognize when a skill applies**: Check if the user's task matches a skill's description
2. **Read the skill's full instructions**: Use the path shown in the skill list above
3. **Follow the skill's instructions**: SKILL.md contains step-by-step workflows, best practices, and examples
4. **Access supporting files**: Skills may include helper scripts, configs, or reference docs - use absolute paths

**When to Use Skills:**
- User's request matches a skill's domain (e.g., "research X" -> web-research skill)
- You need specialized knowledge or structured workflows
- A skill provides proven patterns for complex tasks

**Executing Skill Scripts:**
Skills may contain Python scripts or other executable files. Always use absolute paths from the skill list.

**Example Workflow:**

User: "Can you research the latest developments in quantum computing?"

1. Check available skills -> See "web-research" skill with its path
2. Read the skill using the path shown
3. Follow the skill's research workflow (search -> organize -> synthesize)
4. Use any helper scripts with absolute paths

Remember: Skills make you more capable and consistent. When in doubt, check if a skill exists for the task!

### Important notes:
- Skills are loaded on-demand to save context space
- After loading a skill, its instructions remain available for the conversation
- You can load multiple skills if needed for complex tasks
</skills_system>
`

func builtSkillsSystemPrompt(registry *Registry, skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var (
		content = SKILL_SYSTEM_PROMPT
		buf     = &bytes.Buffer{}
		los     = registry.Locations()
	)

	buf.WriteString("<skills_locations>\n")
	for _, loc := range los {
		buf.WriteString(fmt.Sprintf("<dir_path>%s</dir_path>\n", loc))
	}
	buf.WriteString("</skills_locations>\n")
	content = strings.ReplaceAll(content, "{skills_locations}", buf.String())

	buf.Reset()
	buf.WriteString("<available_skills>\n")
	for _, skill := range skills {
		buf.WriteString("<skill>\n")
		buf.WriteString(fmt.Sprintf("<name>%s</name>\n", skill.Name))
		buf.WriteString(fmt.Sprintf("<description>%s</description>\n", skill.Description))
		buf.WriteString(fmt.Sprintf("<dir_path>%s</dir_path>\n", skill.BasePath))
		buf.WriteString("</skill>\n")
	}
	buf.WriteString("</available_skills>")
	content = strings.ReplaceAll(content, "{skills_list}", buf.String())

	return buf.String()
}
