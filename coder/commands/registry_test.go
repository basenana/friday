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
	want := []string{"archive", "clear", "compact", "context", "copy", "delete", "diff", "help", "loop", "mcp", "model", "open", "plan", "quit", "rename", "resume", "show", "status", "stop", "tasks"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for _, removed := range []string{"new", "session", "sessions", "cost"} {
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

func TestCommandRunPolicies(t *testing.T) {
	reg := NewRegistry()
	RegisterAll(reg)
	immediate := map[string]bool{"context": true, "copy": true, "diff": true, "help": true, "mcp": true, "open": true, "show": true, "status": true, "stop": true, "tasks": true}
	for _, cmd := range reg.List() {
		want := PolicyDeferred
		if immediate[cmd.Name()] {
			want = PolicyImmediate
		}
		if got := CommandMetadata(cmd).Policy; got != want {
			t.Errorf("/%s policy = %q, want %q", cmd.Name(), got, want)
		}
	}
}
