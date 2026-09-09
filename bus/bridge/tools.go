package bridge

import "sync"

// ToolCallTracker maps toolCallID -> sanitized tool name across the
// lifetime of a tool call. It is necessary because ToolCallArgs/End and
// ToolCallResult payloads carry only the tool call id, while the topic
// grammar routes by tool name (known only from ToolCallStart).
type ToolCallTracker struct {
	mu    sync.Mutex
	names map[string]string
}

// NewToolCallTracker builds an empty tracker.
func NewToolCallTracker() *ToolCallTracker {
	return &ToolCallTracker{names: map[string]string{}}
}

// Record stores the name for a tool call id.
func (t *ToolCallTracker) Record(toolCallID, toolName string) {
	if toolCallID == "" {
		return
	}
	t.mu.Lock()
	t.names[toolCallID] = toolName
	t.mu.Unlock()
}

// Lookup returns the recorded name for a tool call id.
func (t *ToolCallTracker) Lookup(toolCallID string) (string, bool) {
	t.mu.Lock()
	name, ok := t.names[toolCallID]
	t.mu.Unlock()
	return name, ok
}

// Evict forgets a tool call id (call after its result was routed).
func (t *ToolCallTracker) Evict(toolCallID string) {
	t.mu.Lock()
	delete(t.names, toolCallID)
	t.mu.Unlock()
}

// Reset forgets everything (call on run boundaries to bound memory).
func (t *ToolCallTracker) Reset() {
	t.mu.Lock()
	t.names = map[string]string{}
	t.mu.Unlock()
}
