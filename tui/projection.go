package tui

import (
	"context"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
)

// transcriptProjection is built off-model so a failed restore cannot leave a
// live TUI half-switched between sessions.
type transcriptProjection struct {
	messages      []chatBlock
	seenInputs    map[string]bool
	cards         map[string]*cardState
	todos         []todoItem
	promptHistory []string
	tokenCount    int
	iteration     int
}

func (m *model) projectTranscript(sessionID string) (transcriptProjection, error) {
	return buildTranscriptProjection(m.runtime, m.cfg, m.workdir, m.width, m.height, sessionID)
}

func buildTranscriptProjection(sessMgr sessionRuntime, cfg *config.Config, workdir string, width, height int, sessionID string) (transcriptProjection, error) {
	p := &model{
		runtime: sessMgr, cfg: cfg, workdir: workdir, width: width, height: height,
		sessionID:  sessionID,
		seenInputs: make(map[string]bool), cards: make(map[string]*cardState),
		toolCalls: make(map[string]int), textBlock: -1, reasonBlock: -1,
		historyIndex: -1, replaying: true,
	}

	var persisted []events.Event
	var replayErr error
	if store, ok := sessMgr.GetStore().(sessions.EventStore); ok {
		persisted, replayErr = store.LoadEvents(context.Background(), sessionID)
	}
	var cutoff int64
	for _, evt := range persisted {
		if evt.Name == events.CustomInputAccepted && !evt.Timestamp.IsZero() {
			cutoff = evt.Timestamp.UnixNano()
			break
		}
	}
	history, err := sessMgr.GetStore().LoadMessages(sessionID)
	if err != nil {
		return transcriptProjection{}, err
	}
	for _, msg := range history {
		if cutoff != 0 && !msg.Time.IsZero() && msg.Time.UnixNano() >= cutoff {
			continue
		}
		p.projectMessage(msg)
	}
	if len(p.toolCalls) > 0 {
		p.flushStreaming(true)
	}
	for _, evt := range persisted {
		p.handleActorEvent(evt)
	}
	if p.form != nil {
		p.appendBlock(chatBlock{kind: blockDivider, content: "unfinished form expired · ask the agent again"})
		p.form = nil
	}
	p.running = false
	p.currentRunID = ""
	p.runStartedAt = time.Time{}
	p.runActivity = ""
	p.resetStreaming()

	return transcriptProjection{
		messages: p.messages, seenInputs: p.seenInputs, cards: p.cards,
		todos: p.todos, promptHistory: p.promptHistory, tokenCount: p.tokenCount, iteration: p.iteration,
	}, replayErr
}

func (m *model) applyProjection(p transcriptProjection) {
	m.messages = p.messages
	m.seenInputs = p.seenInputs
	m.cards = p.cards
	m.todos = append([]todoItem(nil), p.todos...)
	if m.projectMgr == nil {
		m.promptHistory = p.promptHistory
	}
	m.tokenCount = p.tokenCount
	m.iteration = p.iteration
	m.running = false
	m.currentRunID = ""
	m.lastFinishedRun = ""
	m.runStartedAt = time.Time{}
	m.runActivity = ""
	m.form = nil
	m.resetStreaming()
	// The transcript was swapped wholesale: the cached viewport content
	// belongs to the previous session and must not survive the next View.
	m.markTranscriptDirty(true)
}

func (m *model) loadTranscript(sessionID string) error {
	p, err := m.projectTranscript(sessionID)
	m.applyProjection(p)
	return err
}

func (m *model) projectMessage(msg types.Message) {
	switch msg.Role {
	case types.RoleUser:
		m.appendBlock(chatBlock{kind: blockUser, content: msg.Content})
		m.rememberPrompt(msg.Content)
	case types.RoleAssistant:
		if msg.Reasoning != "" {
			m.appendBlock(chatBlock{kind: blockReasoning, content: msg.Reasoning})
		}
		if msg.Content != "" {
			m.appendBlock(chatBlock{kind: blockAssistant, content: msg.Content})
		}
		for _, tc := range msg.ToolCalls {
			m.appendBlock(chatBlock{kind: blockToolCall, id: tc.ID, toolName: tc.Name, toolArgs: tc.Arguments, toolArgsComplete: true, pending: true})
			m.toolCalls[tc.ID] = len(m.messages) - 1
		}
	case types.RoleTool:
		if msg.ToolResult != nil {
			if index, ok := m.toolCalls[msg.ToolResult.CallID]; ok && index >= 0 && index < len(m.messages) {
				block := &m.messages[index]
				block.toolOutput = msg.ToolResult.Content
				block.toolArgsComplete = true
				block.success = msg.ToolResult.Success
				block.timedOut = msg.ToolResult.Status == "timed_out"
				block.timeoutKind = msg.ToolResult.TimeoutKind
				block.pending = false
				block.rendered = ""
				delete(m.toolCalls, msg.ToolResult.CallID)
				m.markTranscriptDirty(true)
			} else {
				m.appendBlock(chatBlock{kind: blockToolCall, id: msg.ToolResult.CallID, toolName: "tool",
					toolOutput: msg.ToolResult.Content, toolArgsComplete: true, success: msg.ToolResult.Success,
					timedOut: msg.ToolResult.Status == "timed_out", timeoutKind: msg.ToolResult.TimeoutKind})
			}
		}
	}
}
