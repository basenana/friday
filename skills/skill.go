package skills

// Frontmatter contains skill metadata used for discovery
type Frontmatter struct {
	Name         string         `yaml:"name"`
	Description  string         `yaml:"description"`
	AllowedTools string         `yaml:"allowed_tools,omitempty"`
	Metadata     map[string]any `yaml:",inline"`
}

// Skill represents a loaded skill with its metadata and instructions
type Skill struct {
	Name         string       // Skill name (from frontmatter)
	Description  string       // Skill description (from frontmatter)
	Frontmatter  *Frontmatter // L1: Metadata for discovery
	Instructions string       // L2: SKILL.md body content (after frontmatter)
	BasePath     string       // Path to skill directory
}

// Resource represents a skill resource file
type Resource struct {
	Name string // Resource filename
	Path string // Relative path within skill directory
}
