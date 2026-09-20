package agents

// Standard tool name constants. These mirror the names registered by the
// sandbox package tools so that ToolPolicy Allow/Deny lists match exactly.
const (
	ToolFsRead    = "fs_read"
	ToolFsWrite   = "fs_write"
	ToolFsList    = "fs_list"
	ToolFsFind    = "fs_find"
	ToolFsSearch  = "fs_search"
	ToolFsDelete  = "fs_delete"
	ToolFsEdit    = "fs_edit"
	ToolBash      = "bash"
	ToolImage     = "image"
	ToolBgTask    = "background_task"
	ToolListTasks = "list_tasks"
	ToolKillTask  = "kill_task"
	ToolWaitTask  = "wait_task"
)

// DefaultAgentName is the name used for the main chat agent when referenced
// from config.
const (
	NameExplorer = "explorer"
)
