package tui

import (
	"context"

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
	promptHistory []string
	tokenCount    int
	iteration     int
}

func (m *model) projectTranscript(sessionID string) (transcriptProjection, error) {
	return buildTranscriptProjection(m.sessMgr, m.cfg, m.workdir, m.width, m.height, sessionID)
}

func buildTranscriptProjection(sessMgr *sessions.Manager, cfg *config.Config, workdir string, width, height int, sessionID string) (transcriptProjection, error) {
	p := &model{
		sessMgr: sessMgr, cfg: cfg, workdir: workdir, width: width, height: height,
		seenInputs: make(map[string]bool), cards: make(map[string]*cardState),
		toolCalls: make(map[string]*toolCallBlock), historyIndex: -1, replaying: true,
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
	if len(p.toolOrder) > 0 {
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
	p.resetStreaming()

	return transcriptProjection{
		messages: p.messages, seenInputs: p.seenInputs, cards: p.cards,
		promptHistory: p.promptHistory, tokenCount: p.tokenCount, iteration: p.iteration,
	}, replayErr
}

func (m *model) applyProjection(p transcriptProjection) {
	m.messages = p.messages
	m.seenInputs = p.seenInputs
	m.cards = p.cards
	m.promptHistory = p.promptHistory
	m.tokenCount = p.tokenCount
	m.iteration = p.iteration
	m.running = false
	m.currentRunID = ""
	m.form = nil
	m.resetStreaming()
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
			m.toolCalls[tc.ID] = &toolCallBlock{id: tc.ID, name: tc.Name, input: tc.Arguments}
			m.toolOrder = append(m.toolOrder, tc.ID)
		}
	case types.RoleTool:
		if msg.ToolResult != nil {
			tc := m.toolCalls[msg.ToolResult.CallID]
			if tc == nil {
				tc = &toolCallBlock{id: msg.ToolResult.CallID, name: "tool"}
			}
			m.appendBlock(chatBlock{kind: blockToolCall, id: tc.id, toolName: tc.name,
				content: joinToolContent(tc.input, msg.ToolResult.Content), success: msg.ToolResult.Success})
			delete(m.toolCalls, msg.ToolResult.CallID)
			m.removeToolOrder(msg.ToolResult.CallID)
		}
	}
}
