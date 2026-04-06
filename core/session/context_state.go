package session

import (
	"time"

	"github.com/basenana/friday/core/types"
)

const (
	defaultRecentFileLimit        = 5
	defaultToolObservationLimit   = 12
	defaultRecentRequestLimit     = 3
	defaultPendingWorkLimit       = 8
	defaultTimelineHighlightLimit = 12
)

type ContextState struct {
	CaseFile         CaseFile
	ToolObservations []ToolObservation
	MemorySnapshot   []types.Message
	PromptBudget     PromptBudget

	MemoryLoaded bool

	LastCompactionAt     time.Time
	LastCompactionTokens int64
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

type ToolObservation struct {
	ToolName   string    `json:"tool_name"`
	Summary    string    `json:"summary"`
	Success    bool      `json:"success"`
	ObservedAt time.Time `json:"observed_at"`
	Files      []string  `json:"files,omitempty"`
}

func newContextState() *ContextState {
	return &ContextState{
		ToolObservations: make([]ToolObservation, 0, defaultToolObservationLimit),
		MemorySnapshot:   make([]types.Message, 0),
	}
}

func restoreContextState(existing *ContextState, history []types.Message) *ContextState {
	state := cloneContextState(existing)
	if state == nil {
		state = newContextState()
	}

	for _, msg := range history {
		if cf, ok := ParseCaseFileMessage(msg.Content); ok {
			state.CaseFile = MergeCaseFiles(state.CaseFile, cf)
		}
	}
	return state
}

func cloneContextState(src *ContextState) *ContextState {
	if src == nil {
		return newContextState()
	}

	dst := *src
	dst.ToolObservations = append([]ToolObservation(nil), src.ToolObservations...)
	dst.MemorySnapshot = append([]types.Message(nil), src.MemorySnapshot...)
	dst.CaseFile = src.CaseFile.clone()
	return &dst
}
