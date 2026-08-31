package actor

import (
	"log"
	"time"

	"github.com/basenana/friday/core/actor/sink"
	coretools "github.com/basenana/friday/core/tools"
)

// Options configures an Actor at construction time.
type Options struct {
	sink                    sink.EventSink
	logger                  *log.Logger
	inboxBuffer             int
	preemptBuffer           int
	filePathValidator       FilePathValidator
	richPathValidator       FilePathValidator
	diffSourcePathValidator FilePathValidator
	unifiedDiffValidator    func([]byte) error

	// TurnLifecycle, when set, is notified on turn boundaries and on
	// each published event. Defaults to a no-op implementation.
	turnLifecycle TurnLifecycle

	// TurnIDGenerator, when set, is invoked at the start of each turn
	// to produce the runID. When nil the actor uses its built-in
	// globalIDGenerator. The actor also consults the inbound
	// UserTextMessage.TurnID field first; this generator is only the
	// fallback for messages without an explicit TurnID.
	turnIDGenerator func() string

	// TurnTimeout caps the duration of a single turn. Zero means no
	// per-turn deadline (the actor relies on the loop ctx only).
	turnTimeout time.Duration

	// ExtraTools are appended to the actor's three card tools on
	// every Chat call. They carry domain tools (MCP, sandbox, skills,
	// etc.) injected by the embedding runtime.
	extraTools []*coretools.Tool
}

func defaultOptions() Options {
	return Options{
		sink:                    sink.Nop(),
		logger:                  nil,
		inboxBuffer:             defaultInboxBuffer,
		preemptBuffer:           defaultPreemptBuffer,
		filePathValidator:       defaultFilePathValidator,
		richPathValidator:       defaultRichPathValidator,
		diffSourcePathValidator: defaultRichPathValidator,
		turnLifecycle:           nopLifecycle{},
	}
}

// WithSink supplies an EventSink for early event sourcing.
func WithSink(s sink.EventSink) Option {
	return func(o *Options) { o.sink = s }
}

// WithLogger attaches a logger used by the inbox / stream for drop
// warnings. Defaults to a discarding logger.
func WithLogger(l *log.Logger) Option {
	return func(o *Options) { o.logger = l }
}

// WithInboxBuffer sizes the inbox channel buffer.
func WithInboxBuffer(n int) Option {
	return func(o *Options) { o.inboxBuffer = n }
}

// WithFilePathValidator overrides validation for legacy file cards and rich
// managed paths before they are emitted to subscribers.
func WithFilePathValidator(v FilePathValidator) Option {
	return func(o *Options) {
		o.filePathValidator = v
		o.richPathValidator = v
		o.diffSourcePathValidator = v
	}
}

// WithDiffSourcePathValidator overrides validation for source-backed diff
// cards without changing validation for other managed rich-card paths.
func WithDiffSourcePathValidator(v FilePathValidator) Option {
	return func(o *Options) {
		if v != nil {
			o.diffSourcePathValidator = v
		}
	}
}

// WithUnifiedDiffValidator validates inline diff content before emission.
func WithUnifiedDiffValidator(v func([]byte) error) Option {
	return func(o *Options) { o.unifiedDiffValidator = v }
}

// WithTurnLifecycle injects a TurnLifecycle that receives turn
// boundary callbacks. See the TurnLifecycle interface for the call
// ordering contract.
func WithTurnLifecycle(l TurnLifecycle) Option {
	return func(o *Options) {
		if l == nil {
			l = nopLifecycle{}
		}
		o.turnLifecycle = l
	}
}

// WithTurnIDGenerator overrides runID generation. The generator is
// consulted when an inbound UserTextMessage carries no TurnID.
func WithTurnIDGenerator(fn func() string) Option {
	return func(o *Options) { o.turnIDGenerator = fn }
}

// WithTurnTimeout caps each turn's duration. On expiry the turn
// context is cancelled and the turn terminates as if preempted.
func WithTurnTimeout(d time.Duration) Option {
	return func(o *Options) { o.turnTimeout = d }
}

// WithExtraTools appends domain tools to the actor's card tools on
// every Chat call. Tools appended here are visible to the agent in
// addition to emit_card / request_form / update_card.
func WithExtraTools(ts ...*coretools.Tool) Option {
	return func(o *Options) { o.extraTools = append(o.extraTools, ts...) }
}

// Option mutates Options during construction.
type Option func(*Options)
