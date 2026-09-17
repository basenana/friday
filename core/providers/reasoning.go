package providers

// Reasoning effort levels for thinking-mode models (e.g. DeepSeek-style APIs).
// ReasoningEffortDefault selects the request's default effort when one is
// available; otherwise providers omit reasoning-related fields.
const (
	ReasoningEffortDefault = "default"
	ReasoningEffortNone    = "none"
	ReasoningEffortLow     = "low"
	ReasoningEffortMedium  = "medium"
	ReasoningEffortHigh    = "high"
	ReasoningEffortXHigh   = "xhigh"
	ReasoningEffortMax     = "max"
)

// IsValidReasoningEffort reports whether v is one of the supported effort levels.
func IsValidReasoningEffort(v string) bool {
	switch v {
	case ReasoningEffortDefault, ReasoningEffortNone, ReasoningEffortLow,
		ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh,
		ReasoningEffortMax:
		return true
	}
	return false
}

// ResolveReasoningEffort returns the effort a provider should use for a
// request. A request-scoped effort has priority over the model configuration.
// The request's default effort is consulted only when that normally selected
// value is empty or explicitly "default".
func ResolveReasoningEffort(req Request, modelEffort string) string {
	effort := RequestReasoningEffort(req)
	if effort == "" {
		effort = modelEffort
	}
	if effort == "" || effort == ReasoningEffortDefault {
		if defaultEffort := RequestDefaultReasoningEffort(req); defaultEffort != "" {
			return defaultEffort
		}
	}
	return effort
}
