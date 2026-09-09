// Package bus defines the topic-based communication layer between
// actors and their consumers (TUI, a2a server, future agent-to-agent
// observation).
//
// Topics are keyed by an encoded session ID (stable across actor
// re-creation), not by actor instance ID. The grammar:
//
//	agent.<sid>.inbox                    # input: user.text / form.submit / form.cancel
//	agent.<sid>.preempt
//	agent.<sid>.run.started|finished|error
//	agent.<sid>.reply.content            # TEXT_MESSAGE_START/CONTENT/END (Event.Type discriminates)
//	agent.<sid>.reply.reasoning          # CUSTOM reasoning.delta
//	agent.<sid>.tool_call.<tool>         # TOOL_CALL_START/ARGS/END
//	agent.<sid>.tool_use.<tool>          # TOOL_CALL_RESULT
//	agent.<sid>.card.<event>             # card.emitted/updated/dismissed
//	agent.<sid>.form.<event>             # form.requested/submitted/cancelled
//	agent.<sid>.obs.<name>               # compact.*/subagent.*/todo.update/model.timeout/loop.start/step
//	agent.<sid>.status.<event>           # created/evicted/stopped/inbox_dropped
//
// The underlying eventbus wildcard "*" matches exactly ONE dot
// section: agent.<sid>.* does NOT match agent.<sid>.reply.content.
// Use SubscribeAgent to subscribe to a session's full traffic; it
// expands the top-level categories.
//
// Delivery semantics: at-most-once, no replay (persistence stays with
// the actor sink), subscribe-before-publish ordering preserved.
package bus

import (
	"sync/atomic"
	"time"

	"github.com/basenana/friday/core/actor/events"
)

// Envelope is the message carried on the bus. It embeds the actor
// event verbatim (AG-UI aligned, payload as json.RawMessage) plus a
// routing header.
type Envelope struct {
	events.Event

	// Topic the envelope was published to (redundant with the routing
	// tables; kept for logs and audits).
	Topic string `json:"topic"`
	// Session is the stable topic key.
	Session string `json:"session"`
	// ActorID identifies the actor instance that produced (or should
	// consume) the message. It changes when the registry re-creates an
	// actor for the same session.
	ActorID string `json:"actorId,omitempty"`
	// From identifies the sender: "user.<id>" or "agent.<sid>".
	From string `json:"from,omitempty"`
	// Seq is a per-actor monotonically increasing sequence assigned by
	// the OutBridge's single goroutine. It resets when an actor is
	// re-created; treat status.created as the epoch boundary.
	Seq uint64 `json:"seq"`
	// TS is a hybrid logical timestamp giving a total order across senders
	// within one process.
	TS int64 `json:"ts"`
	// Depth counts agent-to-agent hops; reserved for future
	// subscribe-observe anti-echo rules. Always 0 in the MVP.
	Depth int `json:"depth,omitempty"`
}

const tsLogicalBits = 20

type hybridClock struct {
	last atomic.Int64
}

// next returns a process-local hybrid logical timestamp. Milliseconds occupy
// the high bits and a CAS increment resolves collisions and wall-clock
// regressions. Unlike UnixNano<<20, this representation does not wrap every
// few hours.
func (c *hybridClock) next(now time.Time) int64 {
	wall := now.UnixMilli() << tsLogicalBits
	for {
		previous := c.last.Load()
		next := wall
		if next <= previous {
			next = previous + 1
		}
		if c.last.CompareAndSwap(previous, next) {
			return next
		}
	}
}

var processClock hybridClock

// NextTS returns a unique, monotonically increasing process-local timestamp.
func NextTS() int64 { return processClock.next(time.Now()) }
