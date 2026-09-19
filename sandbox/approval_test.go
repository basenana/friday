package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
)

// fakePrompter implements FormPrompter with scripted decisions. WaitForForm
// pops the next queued decision value (defaulting to deny); with block set
// it waits for ctx and returns its error, simulating an unanswered form.
type fakePrompter struct {
	mu      sync.Mutex
	queue   []string
	calls   int
	cancels int
	block   bool
	schemas []map[string]any
}

func (f *fakePrompter) EmitCustom(name, itemID string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == events.CustomFormCancelled {
		f.cancels++
	}
	if body, ok := payload.(events.FormRequestedBody); ok {
		f.schemas = append(f.schemas, body.Schema)
	}
}

func (f *fakePrompter) WaitForForm(ctx context.Context, _ string) (coreactor.FormOutcome, error) {
	f.mu.Lock()
	f.calls++
	block := f.block
	value := approvalValueDeny
	if !block && len(f.queue) > 0 {
		value = f.queue[0]
		f.queue = f.queue[1:]
	}
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return coreactor.FormOutcome{}, ctx.Err()
	}
	return coreactor.FormOutcome{Values: map[string]any{approvalFieldDecision: value}}, nil
}

func (f *fakePrompter) stats() (calls int, lastSchema map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.schemas) > 0 {
		lastSchema = f.schemas[len(f.schemas)-1]
	}
	return f.calls, lastSchema
}

func (f *fakePrompter) statsWithCancels() (calls, cancels int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.cancels
}

// approvalTestExecutor builds an executor whose commands run unsandboxed
// (so tests execute real shell builtins) while permission rules still apply.
func approvalTestExecutor(allow, deny []string) *Executor {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = allow
	cfg.Permissions.Deny = deny
	return NewExecutor(cfg)
}

func approvalOverlayPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "projects", "friday-test", "sandbox.json")
}

func TestApproverAllowedCommandRunsDirectly(t *testing.T) {
	exec := approvalTestExecutor([]string{"printf"}, nil)
	fake := &fakePrompter{}
	approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
	approver.Bind(fake)

	result, err := approver.Request(context.Background(), exec, "printf direct", ExecOptions{})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !strings.Contains(result.Stdout, "direct") {
		t.Fatalf("stdout = %q, want command output", result.Stdout)
	}
	if calls, _ := fake.stats(); calls != 0 {
		t.Fatalf("prompt calls = %d, want 0 for allowed command", calls)
	}
}

func TestApproverPersistWritesOverlayAndExecutes(t *testing.T) {
	exec := approvalTestExecutor([]string{"echo"}, nil)
	overlay := approvalOverlayPath(t)
	fake := &fakePrompter{queue: []string{approvalValuePersist}}
	approver := NewCommandApprover(exec.Permission(), overlay)
	approver.Bind(fake)

	result, err := approver.Request(context.Background(), exec, "printf persisted-ok", ExecOptions{})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !strings.Contains(result.Stdout, "persisted-ok") {
		t.Fatalf("stdout = %q, want retried command output", result.Stdout)
	}

	allow, err := LoadProjectAllow(overlay)
	if err != nil {
		t.Fatalf("LoadProjectAllow: %v", err)
	}
	if len(allow) != 1 || allow[0] != "printf" {
		t.Fatalf("overlay allow = %#v, want [printf]", allow)
	}
	if decision, _ := exec.Permission().Check("printf anything"); decision != Allow {
		t.Fatal("grant should be live in the session permission")
	}

	calls, schema := fake.stats()
	if calls != 1 {
		t.Fatalf("prompt calls = %d, want 1", calls)
	}
	if schema == nil {
		t.Fatal("no form.requested schema captured")
	}
	// The questions variant is a UX contract: it must render through the same
	// focused one-question view (up/down selection) as request_user_input.
	// A bespoke variant silently falls back to the generic form renderer.
	if schema["variant"] != "questions" {
		t.Fatalf("variant = %v, want %q", schema["variant"], "questions")
	}
	fields, _ := schema["fields"].([]any)
	if len(fields) != 1 {
		t.Fatalf("fields = %#v, want one decision field", fields)
	}
	field, _ := fields[0].(map[string]any)
	if help, _ := field["help"].(string); !strings.Contains(help, "printf") {
		t.Fatalf("field help = %q, want the denied command name in the question text", help)
	}
	options, _ := field["options"].([]any)
	if len(options) != 3 {
		t.Fatalf("options = %#v, want 3", options)
	}
	first, _ := options[0].(map[string]any)
	if first["value"] != approvalValuePersist {
		t.Fatalf("first option value = %v, want %q (recommended choice first)", first["value"], approvalValuePersist)
	}
	if label, _ := first["label"].(string); len([]rune(label)) > 40 {
		t.Fatalf("first option label %q is too long; keep option labels short", label)
	}
	// The overlay file path must not leak into the form; it is over-detailed
	// for the moment of decision and documented in the README instead.
	if encoded, err := json.Marshal(schema); err == nil && strings.Contains(string(encoded), overlay) {
		t.Fatalf("form schema leaks the overlay path %q", overlay)
	}
}

func TestApproverOnceGrantsSessionOnly(t *testing.T) {
	exec := approvalTestExecutor([]string{"echo"}, nil)
	overlay := approvalOverlayPath(t)
	fake := &fakePrompter{queue: []string{approvalValueOnce}}
	approver := NewCommandApprover(exec.Permission(), overlay)
	approver.Bind(fake)

	result, err := approver.Request(context.Background(), exec, "printf once-ok", ExecOptions{})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !strings.Contains(result.Stdout, "once-ok") {
		t.Fatalf("stdout = %q, want retried command output", result.Stdout)
	}
	if decision, _ := exec.Permission().Check("printf anything"); decision != Allow {
		t.Fatal("one-time grant should be live for the session")
	}
	if allow, err := LoadProjectAllow(overlay); err != nil || allow != nil {
		t.Fatalf("overlay = %#v, err = %v; want no file for once-only grant", allow, err)
	}
}

func TestApproverExplicitDenyDoesNotPrompt(t *testing.T) {
	exec := approvalTestExecutor([]string{"echo"}, DefaultDeniedCommands)
	fake := &fakePrompter{queue: []string{approvalValuePersist}}
	approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
	approver.Bind(fake)

	result, err := approver.Request(context.Background(), exec, DefaultDeniedCommands[0]+" ls", ExecOptions{})
	if !IsDenied(err) {
		t.Fatalf("err = %v, want permission denial", err)
	}
	var denied *DeniedError
	if !errors.As(err, &denied) || !denied.ExplicitDeny {
		t.Fatalf("err = %v, want explicit deny-rule denial", err)
	}
	if result == nil || result.ExitCode == 0 {
		t.Fatalf("result = %+v, want denial result", result)
	}
	if calls, _ := fake.stats(); calls != 0 {
		t.Fatalf("prompt calls = %d, want 0 for explicit deny", calls)
	}
}

func TestApproverUnboundPrompterReturnsHeadlessError(t *testing.T) {
	exec := approvalTestExecutor([]string{"echo"}, nil)
	approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
	// Deliberately no Bind: headless mode.

	_, err := approver.Request(context.Background(), exec, "printf headless", ExecOptions{})
	if !IsDenied(err) {
		t.Fatalf("err = %v, want denial preserving IsDenied semantics", err)
	}
	if !strings.Contains(err.Error(), "interactive approval is unavailable") {
		t.Fatalf("err = %v, want headless hint", err)
	}
}

func TestApproverUserDenyMarksError(t *testing.T) {
	exec := approvalTestExecutor([]string{"echo"}, nil)
	fake := &fakePrompter{queue: []string{approvalValueDeny}}
	approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
	approver.Bind(fake)

	_, err := approver.Request(context.Background(), exec, "printf denied", ExecOptions{})
	if !IsDenied(err) {
		t.Fatalf("err = %v, want denial", err)
	}
	if !IsApprovalDenied(err) {
		t.Fatalf("err = %v, want ErrApprovalDenied marker", err)
	}
	if decision, _ := exec.Permission().Check("printf x"); decision != Deny {
		t.Fatal("deny decision must not grant anything")
	}
}

func TestApproverTimeoutDeniesByDefault(t *testing.T) {
	origTimeout := approvalWaitTimeout
	approvalWaitTimeout = 100 * time.Millisecond
	defer func() { approvalWaitTimeout = origTimeout }()

	exec := approvalTestExecutor([]string{"echo"}, nil)
	fake := &fakePrompter{block: true}
	approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
	approver.Bind(fake)

	started := time.Now()
	_, err := approver.Request(context.Background(), exec, "printf unanswered", ExecOptions{})
	if !IsDenied(err) {
		t.Fatalf("err = %v, want denial preserving IsDenied semantics", err)
	}
	if !IsApprovalTimeout(err) {
		t.Fatalf("err = %v, want ErrApprovalTimeout marker", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout mention", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("Request blocked for %s; the approval timeout did not apply", elapsed)
	}
	if decision, _ := exec.Permission().Check("printf x"); decision != Deny {
		t.Fatal("an unanswered form must not grant anything")
	}
	calls, cancels := fake.statsWithCancels()
	if calls != 1 || cancels != 1 {
		t.Fatalf("prompt calls = %d, cancel events = %d; want the form dismissed after one prompt", calls, cancels)
	}
}

func TestApproverCapsApprovalRounds(t *testing.T) {
	exec := approvalTestExecutor(nil, nil)
	fake := &fakePrompter{queue: []string{approvalValueOnce, approvalValueOnce, approvalValueOnce}}
	approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
	approver.Bind(fake)

	// Four distinct unknown sub-commands: three prompts happen, the fourth
	// denial exhausts the round budget.
	_, err := approver.Request(context.Background(), exec, "trueone && truetwo && truethree && truefour", ExecOptions{})
	if err == nil {
		t.Fatal("expected error after exhausting approval rounds")
	}
	if !strings.Contains(err.Error(), "approval rounds") {
		t.Fatalf("err = %v, want round-cap mention", err)
	}
	if !IsDenied(err) {
		t.Fatalf("err = %v, want wrapped denial", err)
	}
	if calls, _ := fake.stats(); calls != maxApprovalRounds {
		t.Fatalf("prompt calls = %d, want %d", calls, maxApprovalRounds)
	}
}
