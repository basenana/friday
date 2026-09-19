package sandbox

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestPermissionAllow(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"git", "npm", "echo"},
			Deny:  []string{},
		},
	}
	perm := NewPermission(cfg)

	tests := []struct {
		cmd  string
		want Decision
	}{
		{"git status", Allow},
		{"git push origin main", Allow},
		{"npm install", Allow},
		{"npm run build", Allow},
		{"echo hello", Allow},
		{"docker ps", Deny}, // not in allow list
	}

	for _, tt := range tests {
		got, err := perm.Check(tt.cmd)
		if err != nil {
			t.Errorf("Permission.Check(%q) error = %v", tt.cmd, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Permission.Check(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestPermissionDeny(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"git", "npm", "sudo", "rm"},
			Deny:  []string{"sudo", "rm -rf"},
		},
	}
	perm := NewPermission(cfg)

	tests := []struct {
		cmd  string
		want Decision
	}{
		{"git status", Allow},
		{"sudo ls", Deny},      // in deny list
		{"rm -rf /tmp", Deny},  // in deny list
		{"rm file.txt", Allow}, // rm but not -rf
		{"npm install", Allow},
		{"docker ps", Deny}, // not in allow list
	}

	for _, tt := range tests {
		got, err := perm.Check(tt.cmd)
		if err != nil {
			t.Errorf("Permission.Check(%q) error = %v", tt.cmd, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Permission.Check(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestDenyPriority(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"git", "git push *"},
			Deny:  []string{"git push --force"},
		},
	}
	perm := NewPermission(cfg)

	tests := []struct {
		cmd  string
		want Decision
	}{
		{"git status", Allow},
		{"git push origin main", Allow},
		{"sudo ls", Deny},     // sudo is in deny list
		{"rm -rf /tmp", Deny}, // rm -rf is in deny list
	}

	for _, tt := range tests {
		got, err := perm.Check(tt.cmd)
		if err != nil {
			t.Errorf("Permission.Check(%q) error = %v", tt.cmd, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Permission.Check(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestCompoundCommandDeny(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"git", "npm", "rm"},
			Deny:  []string{"sudo"},
		},
	}
	perm := NewPermission(cfg)

	tests := []struct {
		cmd  string
		want Decision
	}{
		{"git status && npm install", Allow},
		{"git status && sudo ls", Deny}, // sudo is denied
		{"rm file && rm other", Allow},
		{"npm install; docker ps", Deny}, // docker not in allow list
	}

	for _, tt := range tests {
		got, err := perm.Check(tt.cmd)
		if err != nil {
			t.Errorf("Permission.Check(%q) error = %v", tt.cmd, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Permission.Check(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestCommandNotInAllow(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"git", "npm"},
			Deny:  []string{},
		},
	}
	perm := NewPermission(cfg)

	tests := []struct {
		cmd  string
		want Decision
	}{
		{"git status", Allow},
		{"npm install", Allow},
		{"docker ps", Deny},        // not in allow list
		{"kubectl get pods", Deny}, // not in allow list
		{"make build", Deny},       // not in allow list
	}

	for _, tt := range tests {
		got, err := perm.Check(tt.cmd)
		if err != nil {
			t.Errorf("Permission.Check(%q) error = %v", tt.cmd, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Permission.Check(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestEmptyAllowDeny(t *testing.T) {
	// Empty allow list means everything is denied
	cfg1 := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{},
			Deny:  []string{},
		},
	}
	perm1 := NewPermission(cfg1)

	got, _ := perm1.Check("echo hello")
	if got != Deny {
		t.Errorf("Empty allow list should deny all, got %v", got)
	}

	// Empty deny list with allow list
	cfg2 := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"echo"},
			Deny:  []string{},
		},
	}
	perm2 := NewPermission(cfg2)

	got, _ = perm2.Check("echo hello")
	if got != Allow {
		t.Errorf("echo should be allowed, got %v", got)
	}
}

func TestCheckWithReason(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"git", "npm"},
			Deny:  []string{"sudo"},
		},
	}
	perm := NewPermission(cfg)

	// Test allowed command
	decision, err := perm.CheckWithReason("git status")
	if err != nil {
		t.Fatalf("CheckWithReason error = %v", err)
	}
	if decision != Allow {
		t.Errorf("git status should be allowed, got %v", decision)
	}

	// Test denied command (not in allow list)
	decision, err = perm.CheckWithReason("docker ps")
	if err == nil {
		t.Fatal("docker ps should return a denial error")
	}
	if decision != Deny {
		t.Errorf("docker ps should be denied, got %v", decision)
	}
	if !IsDenied(err) {
		t.Errorf("denial error should wrap ErrPermissionDenied, got %v", err)
	}
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("denial error should be *DeniedError, got %T", err)
	}
	if denied.Command != "docker" {
		t.Errorf("denied command = %q, want docker", denied.Command)
	}
	if denied.ExplicitDeny {
		t.Error("missing-allowlist denial must not be marked ExplicitDeny")
	}
	if !strings.Contains(denied.Reason, "not in allow list") {
		t.Errorf("reason = %q, want allow-list mention", denied.Reason)
	}

	// Test denied command (in deny list)
	decision, err = perm.CheckWithReason("sudo ls")
	if err == nil {
		t.Fatal("sudo ls should return a denial error")
	}
	if decision != Deny {
		t.Errorf("sudo ls should be denied, got %v", decision)
	}
	if !errors.As(err, &denied) {
		t.Fatalf("denial error should be *DeniedError, got %T", err)
	}
	if denied.Command != "sudo" {
		t.Errorf("denied command = %q, want sudo", denied.Command)
	}
	if !denied.ExplicitDeny {
		t.Error("deny-rule denial must be marked ExplicitDeny")
	}
	if !strings.Contains(denied.Reason, "deny rule") {
		t.Errorf("reason = %q, want deny-rule mention", denied.Reason)
	}
}

func TestSubcommandMatching(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{
				"git",
				"docker ps",
				"docker logs",
				"npm run",
			},
			Deny: []string{
				"git push --force",
				"docker run * --privileged",
			},
		},
	}
	perm := NewPermission(cfg)

	tests := []struct {
		cmd  string
		want Decision
	}{
		{"git status", Allow},
		{"git push origin", Allow},
		{"git push --force", Deny},
		{"docker ps", Allow},
		{"docker logs container", Allow},
		{"docker run ubuntu", Deny}, // docker run not in allow list
		{"docker run --privileged ubuntu", Deny},
		{"npm run build", Allow},
		{"npm install", Deny}, // npm not in allow list (only npm run)
	}

	for _, tt := range tests {
		got, err := perm.Check(tt.cmd)
		if err != nil {
			t.Errorf("Permission.Check(%q) error = %v", tt.cmd, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Permission.Check(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestWildcardMatching(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{
				"npm run *",
				"make *",
			},
			Deny: []string{
				"rm -rf * /",
				"git push * --force",
			},
		},
	}
	perm := NewPermission(cfg)

	tests := []struct {
		cmd  string
		want Decision
	}{
		{"npm run build", Allow},
		{"npm run test", Allow},
		{"npm run lint --fix", Allow},
		{"make build", Allow},
		{"make test", Allow},
		{"rm -rf /tmp/dir", Deny},
		{"rm -rf something /", Deny},
		{"git push origin --force", Deny},
		{"npm install", Deny}, // doesn't match npm run *
	}

	for _, tt := range tests {
		got, err := perm.Check(tt.cmd)
		if err != nil {
			t.Errorf("Permission.Check(%q) error = %v", tt.cmd, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Permission.Check(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestDeniedErrorWrapsErrPermissionDenied(t *testing.T) {
	denied := &DeniedError{Command: "gofmt", Reason: "command 'gofmt' is not in allow list"}
	if !errors.Is(denied, ErrPermissionDenied) {
		t.Fatal("DeniedError should satisfy errors.Is(err, ErrPermissionDenied)")
	}
	if !IsDenied(denied) {
		t.Fatal("IsDenied should accept *DeniedError")
	}
	if !strings.Contains(denied.Error(), "gofmt") {
		t.Errorf("Error() = %q, want command name mention", denied.Error())
	}
}

func TestGrantTakesEffectImmediatelyAndDeduplicates(t *testing.T) {
	cfg := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"echo"},
			Deny:  []string{"sudo"},
		},
	}
	perm := NewPermission(cfg)

	if decision, _ := perm.Check("printf hi"); decision != Deny {
		t.Fatal("printf should be denied before the grant")
	}

	perm.Grant("printf")
	perm.Grant("printf") // duplicate grant must not duplicate the entry

	if decision, _ := perm.Check("printf hi"); decision != Allow {
		t.Fatal("printf should be allowed right after the grant")
	}
	count := 0
	for _, entry := range cfg.Permissions.Allow {
		if entry == "printf" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("printf appears %d times in allow list, want 1", count)
	}
	// Grants never lift deny rules.
	if decision, _ := perm.Check("sudo ls"); decision != Deny {
		t.Fatal("sudo must stay denied after unrelated grants")
	}
	perm.Grant("   ") // empty pattern is ignored
	if len(cfg.Permissions.Allow) != 2 {
		t.Fatalf("allow list = %#v, want exactly echo+printf", cfg.Permissions.Allow)
	}
}

func TestConcurrentCheckAndGrant(t *testing.T) {
	cfg := &Config{Permissions: PermissionsConfig{Allow: []string{"echo"}}}
	perm := NewPermission(cfg)

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				name := fmt.Sprintf("cmd%d_%d", g, i)
				perm.Grant(name)
				if _, err := perm.Check(name + " --flag"); err != nil {
					t.Errorf("Check(%s) error = %v", name, err)
				}
				if _, err := perm.CheckWithReason("echo hi"); err != nil {
					t.Errorf("CheckWithReason error = %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
}
