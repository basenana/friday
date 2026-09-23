package commands

import (
	"reflect"
	"strings"
	"testing"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewRegistry()
	reg.Register(clearCmd{})
	reg.Register(quitCmd{})

	if _, ok := reg.Lookup("clear"); !ok {
		t.Error("Lookup(clear) failed")
	}
	if _, ok := reg.Lookup("quit"); !ok {
		t.Error("Lookup(quit) failed")
	}
}

func TestRegistry_LookupAlias(t *testing.T) {
	reg := NewRegistry()
	reg.Register(quitCmd{})

	// quitCmd has alias "exit"
	if _, ok := reg.Lookup("exit"); !ok {
		t.Error("Lookup(exit) should resolve to quit via alias")
	}
}

func TestRegistry_LookupCaseInsensitive(t *testing.T) {
	reg := NewRegistry()
	reg.Register(clearCmd{})

	if _, ok := reg.Lookup("CLEAR"); !ok {
		t.Error("Lookup should be case-insensitive")
	}
}

func TestRegistry_LookupUnknownReturnsFalse(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Lookup("nonexistent"); ok {
		t.Error("Lookup for unknown should return false")
	}
}

func TestRegistry_ListReturnsAll(t *testing.T) {
	reg := NewRegistry()
	reg.Register(clearCmd{})
	reg.Register(quitCmd{})
	if len(reg.List()) != 2 {
		t.Errorf("List returned %d, want 2", len(reg.List()))
	}
}

func TestRegisterAllDefinesCanonicalCommandSurface(t *testing.T) {
	reg := NewRegistry()
	RegisterAll(reg)
	var got []string
	for _, cmd := range reg.List() {
		got = append(got, cmd.Name())
	}
	want := []string{"archive", "clear", "compact", "context", "copy", "delete", "diff", "effort", "help", "loop", "mcp", "model", "open", "paste-image", "plan", "quit", "rename", "resume", "select", "show", "status", "stop", "tasks", "vscode", "worktree"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for _, removed := range []string{"session", "sessions", "cost"} {
		if _, ok := reg.Lookup(removed); ok {
			t.Errorf("removed command %q is registered", removed)
		}
	}
	help, err := helpCmd{registry: reg}.Execute(nil)
	message := actionAt[AppendMessageAction](t, help, 0).Content
	if err != nil || !strings.Contains(message, "/open") || !strings.Contains(message, "/show") || !strings.Contains(message, "Shift+Tab") {
		t.Fatalf("help is incomplete: %q, err=%v", message, err)
	}
}

func TestWorktreeCommandsPreserveRawArguments(t *testing.T) {
	reg := NewRegistry()
	RegisterSessionCommands(reg)
	newCommand, ok := reg.Lookup("worktree")
	if !ok {
		t.Fatal("/worktree is not registered")
	}
	result, err := newCommand.Execute(&Context{RawArgs: "  implement login flow  "})
	if err != nil {
		t.Fatal(err)
	}
	if got := actionAt[CreateWorktreeAction](t, result, 0).Requirement; got != "implement login flow" {
		t.Fatalf("requirement = %q", got)
	}
	if _, ok := reg.Lookup("new"); ok {
		t.Fatal("/new remains registered")
	}
	selectCommand, ok := reg.Lookup("select")
	if !ok {
		t.Fatal("/select is not registered")
	}
	result, err = selectCommand.Execute(&Context{RawArgs: "  friday/login  "})
	if err != nil {
		t.Fatal(err)
	}
	if got := actionAt[SelectWorktreeAction](t, result, 0).Target; got != "friday/login" {
		t.Fatalf("target = %q", got)
	}
}

func TestVSCodeCommandOpensCurrentWorktree(t *testing.T) {
	reg := NewRegistry()
	RegisterSessionCommands(reg)
	cmd, ok := reg.Lookup("vscode")
	if !ok {
		t.Fatal("/vscode is not registered")
	}
	result, err := cmd.Execute(&Context{})
	if err != nil {
		t.Fatal(err)
	}
	actionAt[ReviewWorktreeAction](t, result, 0)
	if _, ok := reg.Lookup("review"); ok {
		t.Fatal("/review remains registered")
	}
}

func TestCommandRunPolicies(t *testing.T) {
	reg := NewRegistry()
	RegisterAll(reg)
	immediate := map[string]bool{"context": true, "copy": true, "diff": true, "help": true, "mcp": true, "open": true, "paste-image": true, "show": true, "status": true, "stop": true, "tasks": true, "vscode": true}
	navigation := map[string]bool{"select": true, "worktree": true}
	for _, cmd := range reg.List() {
		want := PolicyDeferred
		if immediate[cmd.Name()] {
			want = PolicyImmediate
		} else if navigation[cmd.Name()] {
			want = PolicyNavigation
		}
		if got := CommandMetadata(cmd).Policy; got != want {
			t.Errorf("/%s policy = %q, want %q", cmd.Name(), got, want)
		}
	}
}
