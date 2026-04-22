package types

import (
	"time"
)

type EventType string

const (
	EventAgentStart     EventType = "agent.start"
	EventAgentFinish    EventType = "agent.finish"
	EventLoopStart      EventType = "loop.start"
	EventModelStart     EventType = "model.start"
	EventModelFinish    EventType = "model.finish"
	EventToolStart      EventType = "tool.start"
	EventToolFinish     EventType = "tool.finish"
	EventCompactStart   EventType = "compact.start"
	EventCompactFinish  EventType = "compact.finish"
	EventCompactSkip    EventType = "compact.skip"
	EventSubagentStart  EventType = "subagent.start"
	EventSubagentFinish EventType = "subagent.finish"
	EventTodoUpdate     EventType = "todo.update"
)

type Event struct {
	Type      EventType         `json:"type"`
	SessionID string            `json:"session_id"`
	RootID    string            `json:"root_id"`
	Seq       int64             `json:"seq"`
	Data      map[string]string `json:"data,omitempty"`
	Time      time.Time         `json:"time"`
}

type Delta struct {
	Content   string `json:"content,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}
