package session

import "testing"

func TestPendingRefocusStoreDrainAndClone(t *testing.T) {
	state := newContextState()
	state.StorePendingRefocus("first")
	state.StorePendingRefocus("latest")
	if got := state.DrainPendingRefocus(); got != "latest" {
		t.Fatalf("DrainPendingRefocus() = %q, want latest", got)
	}
	if got := state.DrainPendingRefocus(); got != "" {
		t.Fatalf("second DrainPendingRefocus() = %q, want empty", got)
	}

	state.StorePendingRefocus("do not inherit")
	clone := cloneContextState(state)
	if got := clone.DrainPendingRefocus(); got != "" {
		t.Fatalf("clone inherited pending refocus %q", got)
	}
}
