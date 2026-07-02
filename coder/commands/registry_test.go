package commands

import "testing"

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
