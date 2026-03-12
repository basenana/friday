package workspace

import "github.com/basenana/friday/core/types"

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
	// MemoryHistory contains memory log messages to prepend to conversation history
	MemoryHistory []types.Message
}
