package agents

import "testing"

func TestComposeSystemPromptPlacesAgentAfterWorkspace(t *testing.T) {
	got := ComposeSystemPrompt(" workspace prompt ", " agent prompt ")
	if got != "workspace prompt\n\nagent prompt" {
		t.Fatalf("composed prompt = %q", got)
	}
}
