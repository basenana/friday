package providers

import "testing"

func TestResolveReasoningEffort(t *testing.T) {
	tests := []struct {
		name          string
		requestEffort string
		modelEffort   string
		defaultEffort string
		want          string
	}{
		{name: "provider default without fallback", want: ""},
		{name: "empty uses model", modelEffort: ReasoningEffortHigh, defaultEffort: ReasoningEffortMedium, want: ReasoningEffortHigh},
		{name: "model default uses request default", modelEffort: ReasoningEffortDefault, defaultEffort: ReasoningEffortMedium, want: ReasoningEffortMedium},
		{name: "empty model uses request default", defaultEffort: ReasoningEffortMedium, want: ReasoningEffortMedium},
		{name: "request default uses request default", requestEffort: ReasoningEffortDefault, modelEffort: ReasoningEffortHigh, defaultEffort: ReasoningEffortMedium, want: ReasoningEffortMedium},
		{name: "request explicit beats all defaults", requestEffort: ReasoningEffortLow, modelEffort: ReasoningEffortHigh, defaultEffort: ReasoningEffortMedium, want: ReasoningEffortLow},
		{name: "none is explicit", requestEffort: ReasoningEffortNone, modelEffort: ReasoningEffortHigh, defaultEffort: ReasoningEffortMedium, want: ReasoningEffortNone},
		{name: "default fallback remains provider default", modelEffort: ReasoningEffortDefault, defaultEffort: ReasoningEffortDefault, want: ReasoningEffortDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := NewRequest("system")
			SetRequestReasoningEffort(req, tt.requestEffort)
			SetRequestDefaultReasoningEffort(req, tt.defaultEffort)
			if got := ResolveReasoningEffort(req, tt.modelEffort); got != tt.want {
				t.Fatalf("ResolveReasoningEffort() = %q, want %q", got, tt.want)
			}
		})
	}
}
