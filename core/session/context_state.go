package session

import (
	"sync"
	"time"

	"github.com/basenana/friday/core/types"
)

const (
	defaultRecentFileLimit        = 5
	defaultTimelineHighlightLimit = 12
)

type ContextState struct {
	SessionMemory    []types.Message
	PromptBudget     PromptBudget

	LastCompactionAt     time.Time
	LastCompactionTokens int64

	// LastSyncedAt is the Time of the last history message included in
	// the persisted session memory. Used as the boundary when projecting
	// history with session memory. Zero value means "never synced".
	LastSyncedAt time.Time

	// SessionMemoryGenerating is an atomic flag (0 or 1) preventing concurrent
	// async session memory generation goroutines.
	SessionMemoryGenerating int32

	// PendingMemory holds a freshly generated session memory record from the
	// async goroutine, waiting to be drained by the next BeforeModel call.
	// Typed as `any` to avoid circular import (actual type: *contextmgr.SessionMemoryRecord).
	// Protected by pendingMu since it's written by the async goroutine and read by the main goroutine.
	pendingMu     sync.Mutex
	PendingMemory any

	// TokenCheckpoint stores the last known accurate token count from an LLM response.
	// When PromptTokens > 0, the total context size can be calculated as:
	//   PromptTokens + estimated tokens for messages added since Index
	TokenCheckpoint TokenCheckpoint
}

// TokenCheckpoint records the exact token usage from the last LLM response,
// along with the history length at that point. This enables fast, accurate
// token counting: use the checkpoint value + estimate for new messages only.
type TokenCheckpoint struct {
	Index        int   // len(History) when checkpoint was recorded
	PromptTokens int64 // actual prompt_tokens from LLM response
}

type PromptBudget struct {
	ContextWindow     int64
	CompletionReserve int64
	ToolReserve       int64
	SoftThreshold     int64
	HardThreshold     int64
	TailTarget        int64
}

type FileRef struct {
	Path   string    `json:"path"`
	Source string    `json:"source,omitempty"`
	SeenAt time.Time `json:"seen_at,omitempty"`
}

func newContextState() *ContextState {
	return &ContextState{
		SessionMemory: make([]types.Message, 0),
	}
}

func restoreContextState(existing *ContextState, history []types.Message) *ContextState {
	// Preserve LastSyncedAt before cloning.
	var savedSyncedAt time.Time
	if existing != nil {
		savedSyncedAt = existing.LastSyncedAt
	}

	state := cloneContextState(existing)
	if state == nil {
		state = newContextState()
	}

	state.LastSyncedAt = savedSyncedAt
	return state
}

func cloneContextState(src *ContextState) *ContextState {
	if src == nil {
		return newContextState()
	}

	dst := *src
	dst.SessionMemory = append([]types.Message(nil), src.SessionMemory...)
	dst.pendingMu = sync.Mutex{} // fresh mutex for the clone
	dst.PendingMemory = nil      // fork starts with no pending
	return &dst
}

func (cs *ContextState) StorePendingMemory(v any) {
	cs.pendingMu.Lock()
	cs.PendingMemory = v
	cs.pendingMu.Unlock()
}

func (cs *ContextState) DrainPendingMemory() any {
	cs.pendingMu.Lock()
	defer cs.pendingMu.Unlock()
	v := cs.PendingMemory
	cs.PendingMemory = nil
	return v
}

