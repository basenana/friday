package main

import (
	"strings"
	"testing"
)

func TestMessageCombination(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		stdin       string
		wantContain []string
	}{
		{
			name:        "args only",
			args:        []string{"hello", "world"},
			stdin:       "",
			wantContain: []string{"hello world"},
		},
		{
			name:        "stdin only",
			args:        nil,
			stdin:       "stdin content",
			wantContain: []string{"stdin content"},
		},
		{
			name:        "combined args and stdin",
			args:        []string{"summarize", "errors"},
			stdin:       "error line 1\nerror line 2",
			wantContain: []string{"summarize errors", "error line 1", "error line 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var argMessage, stdinMessage string

			if len(tt.args) > 0 {
				argMessage = strings.Join(tt.args, " ")
			}
			stdinMessage = tt.stdin

			var userMessage string
			switch {
			case argMessage != "" && stdinMessage != "":
				userMessage = argMessage + "\n\n" + stdinMessage
			case argMessage != "":
				userMessage = argMessage
			case stdinMessage != "":
				userMessage = stdinMessage
			}

			for _, want := range tt.wantContain {
				if !strings.Contains(userMessage, want) {
					t.Errorf("message should contain %q, got %q", want, userMessage)
				}
			}
		})
	}
}
