package proposals

import "testing"

func TestProposalRunContractDerivesTitle(t *testing.T) {
	tool := ProposalRunTool(nil, nil)
	if _, exists := tool.InputSchema.Properties["title"]; exists {
		t.Fatal("title should be derived from content")
	}
	if _, exists := tool.InputSchema.Properties["content"]; !exists {
		t.Fatal("content field missing")
	}
}
