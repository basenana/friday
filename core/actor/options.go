package actor

import (
	"time"

	"github.com/basenana/friday/core/actor/sink"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/planning"
	coretools "github.com/basenana/friday/core/tools"
)

// Options configures an Actor at construction time.
type Options struct {
	sink                    sink.EventSink
	logger                  logger.Logger
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
	// UserTextMessage.TurnID or AgentTextMessage.TurnID field first; this
	// generator is only the fallback for inputs without an explicit TurnID.
	turnIDGenerator func() string

	// TurnTimeout caps the duration of a single turn. Zero means no
	// per-turn deadline (the actor relies on the loop ctx only).
	turnTimeout time.Duration

	// ExtraTools are appended to the actor's three card tools on
	// every Chat call. They carry domain tools (MCP, sandbox, skills,
	// etc.) injected by the embedding runtime.
	extraTools     []*coretools.Tool
	modeProvider   collaboration.ModeProvider
	modeController collaboration.ModeController
	planRepository planning.Repository
	agentPlanEntry bool
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

// WithLogger attaches a project logger used by the inbox / stream for drop
// warnings. The process root determines its destination.
func WithLogger(l logger.Logger) Option {
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

// WithTurnIDGenerator overrides runID generation. The generator is consulted
// when an inbound user or agent text message carries no TurnID.
func WithTurnIDGenerator(fn func() string) Option {
	return func(o *Options) { o.turnIDGenerator = fn }
}

// WithTurnTimeout caps each turn's duration. On expiry the turn
// context is cancelled and the turn terminates as if preempted.
func WithTurnTimeout(d time.Duration) Option {
	return func(o *Options) { o.turnTimeout = d }
}

// WithExtraTools adds domain tools to every actor Chat call.
func WithExtraTools(ts ...*coretools.Tool) Option {
	return func(o *Options) { o.extraTools = append(o.extraTools, ts...) }
}

// WithPlanning enables collaboration-mode-aware planning tools.
func WithPlanning(modes collaboration.ModeProvider, plans planning.Repository) Option {
	return func(o *Options) {
		o.modeProvider = modes
		o.modeController, _ = modes.(collaboration.ModeController)
		o.planRepository = plans
	}
}

// WithAgentPlanEntry exposes enter_plan_mode so the model may initiate a
// planning handoff. Enable it only for clients that can present and resolve
// the resulting plan approval interaction.
func WithAgentPlanEntry(enabled bool) Option {
	return func(o *Options) { o.agentPlanEntry = enabled }
}

// Option mutates Options during construction.
type Option func(*Options)
