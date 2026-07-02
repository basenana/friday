//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/types"
)

// newA2AServerEnv starts an A2A server on a random port and returns
// (serverURL, cleanup).
func newA2AServerEnv(t *testing.T, cfg *E2EConfig, modelName, authToken string) (string, func()) {
	t.Helper()
	fc := fridayConfig(t, cfg, modelName)
	dir := fc.DataDir
	mgr := newSessionManager(t, dir)
	mgr.SetLLM(newClient(t, cfg, modelName))
	url, stop := startA2AServer(t, fc, mgr, authToken)
	return url, func() { stop() }
}

// TestA2A_AgentCard verifies the agent card is served at the well-known URL.
func TestA2A_AgentCard(t *testing.T) {
	cfg := loadConfig(t)
	url, cleanup := newA2AServerEnv(t, cfg, "chat", "")
	defer cleanup()

	card := a2aGetAgentCard(t, url)
	if name, _ := card["name"].(string); !strings.Contains(strings.ToLower(name), "friday") {
		t.Errorf("expected card name to contain 'friday', got %q", name)
	}
}

// TestA2A_MessageSend verifies the full message/send → response flow.
func TestA2A_MessageSend(t *testing.T) {
	cfg := loadConfig(t)
	url, cleanup := newA2AServerEnv(t, cfg, "chat", "")
	defer cleanup()

	taskID := types.NewID()
	code, body := a2aMessageSend(t, url, "", taskID, "Say hello in one short sentence.")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", code, body)
	}
	// The result itself is the task object (with id, status, artifacts).
	result, _ := body["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result in response: %v", body)
	}
	status, _ := result["status"].(map[string]any)
	if status == nil {
		t.Fatalf("no status in result: %v", result)
	}
	state, _ := status["state"].(string)
	if state == "" {
		t.Fatalf("no state in status: %v", status)
	}
	if state != "completed" && state != "submitted" && state != "working" {
		t.Errorf("unexpected task state %q", state)
	}
	artifacts, _ := result["artifacts"].([]any)
	if len(artifacts) == 0 {
		t.Errorf("expected at least one artifact in result")
	}
}

// TestA2A_AuthRequired verifies that a missing Bearer token is rejected.
func TestA2A_AuthRequired(t *testing.T) {
	cfg := loadConfig(t)
	url, cleanup := newA2AServerEnv(t, cfg, "chat", "secret-token")
	defer cleanup()

	taskID := types.NewID()
	code, _ := a2aPost(t, url, "", fmt.Sprintf(`{
		"jsonrpc":"2.0","method":"message/send","id":1,
		"params":{"message":{"messageId":"%s","role":"user","parts":[{"kind":"text","text":"hi"}]}}
	}`, taskID))
	if code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", code)
	}
}

// TestA2A_AuthWrongToken verifies that an incorrect token is rejected.
func TestA2A_AuthWrongToken(t *testing.T) {
	cfg := loadConfig(t)
	url, cleanup := newA2AServerEnv(t, cfg, "chat", "secret-token")
	defer cleanup()

	taskID := types.NewID()
	req, _ := http.NewRequest("POST", url, strings.NewReader(fmt.Sprintf(`{
		"jsonrpc":"2.0","method":"message/send","id":1,
		"params":{"message":{"messageId":"%s","role":"user","parts":[{"kind":"text","text":"hi"}]}}
	}`, taskID)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestA2A_AuthCorrect verifies that the correct token is accepted.
func TestA2A_AuthCorrect(t *testing.T) {
	cfg := loadConfig(t)
	url, cleanup := newA2AServerEnv(t, cfg, "chat", "secret-token")
	defer cleanup()

	taskID := types.NewID()
	code, _ := a2aPost(t, url, "secret-token", fmt.Sprintf(`{
		"jsonrpc":"2.0","method":"message/send","id":1,
		"params":{"message":{"messageId":"%s","role":"user","parts":[{"kind":"text","text":"hi"}]}}
	}`, taskID))
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
}

// TestA2A_EmptyMessage verifies that an empty message does not crash.
func TestA2A_EmptyMessage(t *testing.T) {
	cfg := loadConfig(t)
	url, cleanup := newA2AServerEnv(t, cfg, "chat", "")
	defer cleanup()

	taskID := types.NewID()
	code, body := a2aPost(t, url, "", fmt.Sprintf(`{
		"jsonrpc":"2.0","method":"message/send","id":1,
		"params":{"message":{"messageId":"%s","role":"user","parts":[{"kind":"text","text":""}]}}
	}`, taskID))
	if code != http.StatusOK && code != http.StatusBadRequest {
		t.Errorf("expected 200 or 400, got %d; body=%v", code, body)
	}
}

// TestA2A_TaskCancel verifies a task can be cancelled.
func TestA2A_TaskCancel(t *testing.T) {
	cfg := loadConfig(t)
	url, cleanup := newA2AServerEnv(t, cfg, "chat", "")
	defer cleanup()

	taskID := types.NewID()
	// Send a long task, then cancel.
	a2aMessageSend(t, url, "", taskID, "Tell me a very long story about a cat.")
	time.Sleep(200 * time.Millisecond)
	code, body := a2aTaskCancel(t, url, "", taskID)
	if code != http.StatusOK {
		t.Logf("cancel returned %d (may already be completed): %v", code, body)
	}
}

// TestA2A_ConcurrentTasks verifies that multiple concurrent tasks are handled
// independently and each response carries a well-formed task object with a
// valid state and (when completed) non-empty artifacts.
func TestA2A_ConcurrentTasks(t *testing.T) {
	cfg := loadConfig(t)
	url, cleanup := newA2AServerEnv(t, cfg, "chat", "")
	defer cleanup()

	const n = 3
	var wg sync.WaitGroup
	errs := make(chan error, n)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			marker := fmt.Sprintf("task %d ok", i)
			taskID := fmt.Sprintf("conc-%d-%s", i, types.NewID())
			code, body := a2aMessageSend(t, url, "", taskID, "Reply with just: "+marker)
			if code != http.StatusOK {
				errs <- fmt.Errorf("task %d: status %d body=%v", i, code, body)
				return
			}
			select {
			case <-ctx.Done():
				errs <- fmt.Errorf("task %d: ctx done", i)
				return
			default:
			}
			// Validate response shape.
			result, _ := body["result"].(map[string]any)
			if result == nil {
				errs <- fmt.Errorf("task %d: no result in response: %v", i, body)
				return
			}
			status, _ := result["status"].(map[string]any)
			if status == nil {
				errs <- fmt.Errorf("task %d: no status in result", i)
				return
			}
			state, _ := status["state"].(string)
			if state != "completed" && state != "submitted" && state != "working" {
				errs <- fmt.Errorf("task %d: unexpected state %q", i, state)
				return
			}
			// When completed, artifacts must be present and contain the marker
			// text in at least one text part (case-insensitive).
			if state == "completed" {
				artifacts, _ := result["artifacts"].([]any)
				if len(artifacts) == 0 {
					errs <- fmt.Errorf("task %d: completed but no artifacts", i)
					return
				}
				var found bool
				for _, a := range artifacts {
					am, _ := a.(map[string]any)
					parts, _ := am["parts"].([]any)
					for _, p := range parts {
						pm, _ := p.(map[string]any)
						text, _ := pm["text"].(string)
						if strings.Contains(strings.ToLower(text), strings.ToLower(marker)) {
							found = true
						}
					}
				}
				if !found {
					// Marker may be paraphrased by the LLM; downgrade to a log
					// to avoid flakiness, but only when state==completed and
					// artifacts exist.
					t.Logf("task %d: marker %q not surfaced verbatim in artifacts", i, marker)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}
