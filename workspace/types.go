package workspace

// FileRole determines how a workspace file is used
type FileRole string

const (
	// FileRoleSystemPrompt files are loaded into the system prompt
	FileRoleSystemPrompt FileRole = "system_prompt"
	// FileRoleGuidance files provide guidance only (not loaded into context)
	FileRoleGuidance FileRole = "guidance"
	// FileRoleOptional files are optional and may be skipped
	FileRoleOptional FileRole = "optional"
)

// FileSpec defines a workspace file's behavior
type FileSpec struct {
	Name     string   // Filename (e.g., "AGENTS.md")
	Role     FileRole // How it's used
	Required bool     // Whether to error if missing (always false for workspace)
}

// LoadedContent represents loaded workspace content ready for use
type LoadedContent struct {
	// SystemPrompts contains content from files with FileRoleSystemPrompt
	SystemPrompts []string
}

// Paths contains important directory paths for the friday application
type Paths struct {
	DataDir   string
	Workspace string
	Sessions  string
	Memory    string
	State     string
}

// SystemInfo contains system environment information
type SystemInfo struct {
	OS       string
	Arch     string
	Hostname string
}

// TemplateParams contains parameters for rendering workspace templates
type TemplateParams struct {
	Paths  *Paths
	System *SystemInfo
}
