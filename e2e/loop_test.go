//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/bus"
	coderloop "github.com/basenana/friday/coder/loop"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
)

// TestCoderLoop_AutonomousFix exercises the same registry, bus, hooks, tools,
// persistence, and model path used by the TUI while keeping filesystem writes
// inside an isolated temporary project.
func TestCoderLoop_AutonomousFix(t *testing.T) {
	cfg := loadConfig(t)
	fc := fridayConfig(t, cfg, "chat")
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "go.mod"), []byte("module loopsmoke\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "calc.go"), []byte("package loopsmoke\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "calc_test.go"), []byte("package loopsmoke\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(2, 3) != 5 { t.Fatal(\"bad sum\") } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := newSessionManager(t, fc.DataDir)
	mgr.SetLLM(newClient(t, cfg, "chat"))
	regCfg := actor.DefaultRegistryConfig()
	regCfg.Workdir = workdir
	reg, err := actor.NewRegistry(mgr, fc, regCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.ShutdownAll()

	sessionID := types.NewID()
	if _, _, err := mgr.GetOrCreateByID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.GetOrCreate(sessionID); err != nil {
		t.Fatal(err)
	}
	lifecycle, ok := reg.Lifecycle(sessionID)
	if !ok || lifecycle.Current() == nil {
		t.Fatal("root lifecycle unavailable")
	}
	sess := lifecycle.Current()
	feed := bus.SubscribeAgentFeed(reg.Bus(), sessionID)
	defer feed.Close()
	loopManager := coderloop.NewManager(reg.Bus())
	defer loopManager.Close()

	if err := loopManager.Start(context.Background(), sess,
		"Fix the failing Add test with the smallest correct change, run the Go tests, and finish the loop when the project is green."); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(cfg.TestTimeout())
	defer deadline.Stop()
	var sawLoopInput, sawFinished bool
	for !sawFinished {
		select {
		case evt := <-feed.Events():
			if evt.Name == events.CustomInputAccepted {
				var body events.InputAcceptedBody
				if events.DecodePayload(evt, &body) == nil {
					for _, source := range body.Sources {
						if source == "loop" {
							sawLoopInput = true
						}
					}
				}
			}
			if evt.Type == events.KindRunFinished {
				raw, err := sess.ReadRecord(context.Background(), coderloop.StateNamespace)
				if err == nil && strings.TrimSpace(string(raw)) == string(coderloop.StateCompleted) {
					sawFinished = true
				}
			}
		case <-time.After(20 * time.Millisecond):
			raw, err := sess.ReadRecord(context.Background(), coderloop.StateNamespace)
			if err == nil && strings.TrimSpace(string(raw)) == string(coderloop.StateCompleted) {
				sawFinished = true
			}
		case <-deadline.C:
			raw, _ := sess.ReadRecord(context.Background(), coderloop.StateNamespace)
			t.Fatalf("loop did not complete before timeout; state=%q", raw)
		}
	}
	if !sawLoopInput {
		t.Fatal("input.accepted did not preserve the loop producer")
	}

	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = workdir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("loop left project tests failing: %v\n%s", err, output)
	}
	got, err := os.ReadFile(filepath.Join(workdir, "calc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "return a + b") {
		t.Fatalf("unexpected implementation:\n%s", got)
	}
	note, err := sess.ReadRecord(context.Background(), coderloop.WorkingNoteNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(note), "Fix the failing Add test") {
		t.Fatalf("Working Note lost the original request:\n%s", note)
	}
	if !historyHasToolCall(sess, "working_note_read", 1) {
		t.Fatal("agent did not read the Working Note")
	}
	if !historyHasToolCall(sess, "finish_devloop", 1) {
		t.Fatal("agent did not hand completed development to review through finish_devloop")
	}
	if !historyHasToolCall(sess, "finish_reviewloop", 1) {
		t.Fatal("agent did not finish final review through finish_reviewloop")
	}
}
