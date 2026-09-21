package codebase

import (
	"encoding/base64"
	"sort"
	"sync"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

type Mode string

const (
	ModeContext Mode = "context"
	ModeIndex   Mode = "index"
)

type ActivityState string

const (
	ActivityRunning   ActivityState = "running"
	ActivitySucceeded ActivityState = "succeeded"
	ActivityFailed    ActivityState = "failed"
	ActivityCancelled ActivityState = "cancelled"
	ActivityTimedOut  ActivityState = "timed_out"
)

type Activity struct {
	ProjectID   string        `json:"project_id"`
	OperationID string        `json:"operation_id"`
	Revision    uint64        `json:"revision"`
	Mode        Mode          `json:"mode"`
	State       ActivityState `json:"state"`
	Phase       string        `json:"phase,omitempty"`
	Summary     string        `json:"summary,omitempty"`
	Detail      string        `json:"detail,omitempty"`
	Error       string        `json:"error,omitempty"`
	SessionID   string        `json:"session_id,omitempty"`
	Turn        uint64        `json:"turn,omitempty"`
	Trigger     string        `json:"trigger,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
}

type activityPublisher struct {
	mu           sync.Mutex
	bus          *eventbus.Bus
	projectID    string
	contextTopic string
	indexTopic   string
	current      map[string]Activity
}

func newActivityPublisher(b *eventbus.Bus, projectID string) *activityPublisher {
	segment := "p_" + base64.RawURLEncoding.EncodeToString([]byte(projectID))
	prefix := "codebase." + segment + ".activity."
	return &activityPublisher{bus: b, projectID: projectID, contextTopic: prefix + "context", indexTopic: prefix + "index", current: map[string]Activity{}}
}

func (p *activityPublisher) feed() *bus.Feed {
	return bus.NewFeed(p.bus, eventbus.SerialConfig{Buffer: 512, Overflow: eventbus.OverflowDropOldest}, p.contextTopic, p.indexTopic)
}

func (p *activityPublisher) publish(a Activity) Activity {
	p.mu.Lock()
	a.ProjectID = p.projectID
	a.Phase = boundText(a.Phase, 200)
	a.Summary = boundText(a.Summary, 500)
	a.Detail = boundText(a.Detail, 2000)
	a.Error = boundText(a.Error, 2000)
	a.Revision = p.current[a.OperationID].Revision + 1
	p.current[a.OperationID] = a
	topic := p.contextTopic
	if a.Mode == ModeIndex {
		topic = p.indexTopic
	}
	evt := events.NewEvent(events.KindCustom, a.OperationID).WithName("codebase.activity").WithPayload(a)
	p.bus.Publish(topic, bus.Envelope{Event: evt, Topic: topic, Session: p.projectID, From: "codebase", TS: bus.NextTS()})
	p.mu.Unlock()
	if a.State != ActivityRunning {
		revision := a.Revision
		time.AfterFunc(activityRetention(a.State), func() {
			p.mu.Lock()
			if current, ok := p.current[a.OperationID]; ok && current.Revision == revision && current.State != ActivityRunning {
				delete(p.current, a.OperationID)
			}
			p.mu.Unlock()
		})
	}
	return a
}

func activityRetention(state ActivityState) time.Duration {
	if state == ActivitySucceeded || state == ActivityCancelled {
		return 5 * time.Second
	}
	return 10 * time.Second
}

func (p *activityPublisher) snapshot() []Activity {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Activity, 0, len(p.current))
	for _, a := range p.current {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OperationID < out[j].OperationID })
	return out
}
