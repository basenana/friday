package shellcmd

import (
	"os/exec"
	"strings"
	"testing"
)

func TestJoinPreservesLiteralArgs(t *testing.T) {
	args := []string{
		"-c",
		`printf '%s\n' "$1" "$2" "$3"`,
		"sh",
		"value with spaces",
		`$(echo should-stay-literal)`,
		`quote'"semi;end`,
	}

	cmd := exec.Command("bash", "-c", Join("/bin/sh", args...))
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	got := strings.Split(strings.TrimSpace(string(output)), "\n")
	want := []string{args[3], args[4], args[5]}
	if len(got) != len(want) {
		t.Fatalf("output lines = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}
