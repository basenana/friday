package providers

// Reasoning effort levels for thinking-mode models (e.g. DeepSeek-style APIs).
// ReasoningEffortDefault means "do not send any reasoning-related fields".
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
