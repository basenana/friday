// Package sink defines the EventSink abstraction used by the actor for
// early-stage event sourcing. The interface is intentionally minimal:
// Append-only is required for the MVP; read-back / replay are left for
// a later iteration.
package sink

import (
	"context"

	"github.com/basenana/friday/core/actor/events"
)

// EventSink is an append-only event log. Implementations must be
// goroutine-safe.
type EventSink interface {
	// Append writes one event to the log. A nil error does not
	// guarantee durability unless the implementation documents stronger
	// semantics.
	Append(ctx context.Context, evt events.Event) error
	// Close releases any resources held by the sink. It must be
	// idempotent.
	Close() error
}

// Nop returns a sink that discards every event. This is the default
// when no sink is configured.
func Nop() EventSink { return nopSink{} }

type nopSink struct{}

func (nopSink) Append(context.Context, events.Event) error { return nil }
func (nopSink) Close() error                               { return nil }
