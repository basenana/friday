//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions/file"
)

// TestSession_Persistence verifies that conversation history is written to
// the file store and can be reloaded.
func TestSession_Persistence(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	dir := t.TempDir()

	store := file.NewFileSessionStore(dir)
	if err := store.EnsureDir(); err != nil {
		t.Fatalf("store.EnsureDir: %v", err)
	}
	sessID := types.NewID()
	// The file store's AppendMessages needs the per-session directory to
	// exist (normally created by store.Create). Pre-create it here so the
	// writer works without going through Create.
	if err := os.MkdirAll(filepath.Join(dir, sessID), 0755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessID, "history.jsonl"), []byte{}, 0644); err != nil {
		t.Fatalf("touch history: %v", err)
	}
	sess1 := session.New(sessID, client, session.WithMessageWriter(store))

	tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
	agent1 := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 15})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	// First turn: tell the agent a fact.
	resp := agent1.Chat(ctx, &api.Request{Session: sess1, UserMessage: "Remember that the project codename is ORANGE_FALCON. Acknowledge briefly."})
	collectResponse(t, ctx, resp)

	// Reload: create a fresh session and inject the persisted history.
	history, err := store.LoadMessages(sessID)
	if err != nil {
		t.Fatalf("load messages: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("expected persisted history, got none")
	}

	sess2 := session.New(types.NewID(), client)
	sess2.ReplaceHistory(history...)

	agent2 := newReactAgent(t, client, agentOpts{MaxLoops: 15})
	resp2 := agent2.Chat(ctx, &api.Request{Session: sess2, UserMessage: "What is the project codename I told you about earlier?"})
	content, _ := collectResponse(t, ctx, resp2)

	if !strings.Contains(strings.ToUpper(content), "ORANGE_FALCON") {
		t.Errorf("expected response to contain ORANGE_FALCON, got %q", truncate(content, 300))
	}
}

// TestSession_Fork verifies that forking a session creates an isolated child.
func TestSession_Fork(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	sess := newTestSession(t, client)

	sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "hello"})
	sess.AppendMessage(&types.Message{Role: types.RoleAssistant, Content: "hi"})

	parentLen := len(sess.GetHistory())
	child := sess.Fork()
	if len(child.GetHistory()) != parentLen {
		t.Errorf("child history len = %d, want %d", len(child.GetHistory()), parentLen)
	}

	child.AppendMessage(&types.Message{Role: types.RoleUser, Content: "child only"})

	if len(sess.GetHistory()) != parentLen {
		t.Errorf("parent history changed after child mutation: %d != %d", len(sess.GetHistory()), parentLen)
	}
	if len(child.GetHistory()) != parentLen+1 {
		t.Errorf("child history len = %d, want %d", len(child.GetHistory()), parentLen+1)
	}
}

// TestSession_EventPublish verifies that core session events are published.
func TestSession_EventPublish(t *testing.T) {
	cfg := loadConfig(t)
	agent, sess, _ := newAgentWithTools(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	eventsPtr, eventsMu, unsub := subscribeSessionEvents(t, sess)
	defer unsub()

	resp := agent.Chat(ctx, &api.Request{Session: sess, UserMessage: "Say hello."})
	collectResponse(t, ctx, resp)

	if !waitForEventType(eventsPtr, eventsMu, types.EventAgentFinish, 5*time.Second) {
		t.Fatal("EventAgentFinish not received within 5s")
	}

	events := getEvents(eventsPtr, eventsMu)
	assertEventReceived(t, events, types.EventAgentStart)
	assertEventReceived(t, events, types.EventModelStart)
	assertEventReceived(t, events, types.EventModelFinish)
	assertEventReceived(t, events, types.EventAgentFinish)
}

// TestSession_ReplaceHistory verifies that ReplaceHistory takes effect.
func TestSession_ReplaceHistory(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	sess := newTestSession(t, client)

	for i := 0; i < 10; i++ {
		sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "msg"})
	}
	if len(sess.GetHistory()) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(sess.GetHistory()))
	}

	sess.ReplaceHistory(
		types.Message{Role: types.RoleUser, Content: "only"},
		types.Message{Role: types.RoleAssistant, Content: "one"},
		types.Message{Role: types.RoleUser, Content: "turn"},
	)
	if len(sess.GetHistory()) != 3 {
		t.Errorf("expected 3 messages after replace, got %d", len(sess.GetHistory()))
	}
}
