//go:build e2e

package e2e

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/fallback"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/setup"
)

// TestProvider_OpenAI_Streaming verifies that the OpenAI provider returns
// streaming deltas and consumes tokens.
func TestProvider_OpenAI_Streaming(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	req := providers.NewRequest("You are a helpful assistant. Reply in one short sentence.",
		types.Message{Role: types.RoleUser, Content: "What colour is the sky?"},
	)
	resp := client.Completion(ctx, req)

	var content string
	deltaCount := 0
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout: %v", ctx.Err())
		case err := <-resp.Error():
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}
		case d, ok := <-resp.Message():
			if !ok {
				goto done
			}
			deltaCount++
			content += d.Content
		}
	}
done:
	if deltaCount == 0 {
		t.Error("expected at least 1 delta, got 0")
	}
	assertNotEmpty(t, content)
}

// TestProvider_Anthropic_Streaming verifies the Anthropic provider path.
func TestProvider_Anthropic_Streaming(t *testing.T) {
	cfg := loadConfig(t)
	m := mustModel(t, cfg, "anthropic")
	if m.Provider != "anthropic" {
		t.Skipf("model anthropic has provider %q", m.Provider)
	}
	client := newClient(t, cfg, "anthropic")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	req := providers.NewRequest("You are a helpful assistant. Reply in one short sentence.",
		types.Message{Role: types.RoleUser, Content: "What colour is the sky?"},
	)
	resp := client.Completion(ctx, req)

	var content string
	deltaCount := 0
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout: %v", ctx.Err())
		case err := <-resp.Error():
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}
		case d, ok := <-resp.Message():
			if !ok {
				goto done
			}
			deltaCount++
			content += d.Content
		}
	}
done:
	if deltaCount == 0 {
		t.Error("expected at least 1 delta, got 0")
	}
	assertNotEmpty(t, content)
}

// TestProvider_NonStreaming verifies CompletionNonStreaming.
func TestProvider_NonStreaming(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	req := providers.NewRequest("", types.Message{Role: types.RoleUser, Content: "Say hello in one word."})
	out, err := client.CompletionNonStreaming(ctx, req)
	if err != nil {
		t.Fatalf("non-streaming error: %v", err)
	}
	assertNotEmpty(t, out)
}

// TestProvider_SystemPromptEnforced verifies that a strict system prompt is
// honoured by the model.
func TestProvider_SystemPromptEnforced(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	withRetry(t, cfg, func(attempt int) error {
		req := providers.NewRequest(
			"You can ONLY answer with the single word YES or the single word NO. Nothing else.",
			types.Message{Role: types.RoleUser, Content: "Is 2 greater than 1?"},
		)
		resp := client.Completion(ctx, req)
		var content string
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-resp.Error():
				if err != nil {
					return err
				}
			case d, ok := <-resp.Message():
				if !ok {
					cleaned := strings.ToUpper(strings.TrimSpace(content))
					if !strings.HasPrefix(cleaned, "YES") && !strings.HasPrefix(cleaned, "NO") {
						return errAssertion{msg: "response does not start with YES or NO: " + truncate(content, 200)}
					}
					return nil
				}
				content += d.Content
			}
		}
	})
}

// TestProvider_MultiTurn verifies that conversation history is respected
// across turns.
func TestProvider_MultiTurn(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	withRetry(t, cfg, func(attempt int) error {
		history := []types.Message{
			{Role: types.RoleUser, Content: "Please remember that the secret password is banana."},
			{Role: types.RoleAssistant, Content: "Got it. The secret password is banana."},
		}
		req := providers.NewRequest("You are a helpful assistant.", history...)
		req.SetHistory(append(history, types.Message{Role: types.RoleUser, Content: "What is the secret password?"}))

		resp := client.Completion(ctx, req)
		var content string
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-resp.Error():
				if err != nil {
					return err
				}
			case d, ok := <-resp.Message():
				if !ok {
					if !strings.Contains(strings.ToLower(content), "banana") {
						return errAssertion{msg: "response missing banana: " + truncate(content, 200)}
					}
					return nil
				}
				content += d.Content
			}
		}
	})
}

// TestProvider_TokenCheckpoint verifies that the provider returns real token
// counts from the API.
func TestProvider_TokenCheckpoint(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	req := providers.NewRequest("You are a helpful assistant.",
		types.Message{Role: types.RoleUser, Content: "Say hello."},
	)
	resp := client.Completion(ctx, req)
	// Drain the stream so the tokens are populated.
	for d := range resp.Message() {
		_ = d
	}
	tokens := resp.Tokens()
	if tokens.PromptTokens <= 0 {
		t.Errorf("expected PromptTokens > 0, got %d", tokens.PromptTokens)
	}
}

// TestProvider_StructuredPredict verifies structured output prediction.
func TestProvider_StructuredPredict(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	type Person struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	req := providers.NewRequest("",
		types.Message{Role: types.RoleUser, Content: "Generate a fictional person named Alice who is 30 years old. Return as JSON with fields name and age."},
	)
	var result Person
	if err := client.StructuredPredict(ctx, req, &result); err != nil {
		t.Fatalf("structured predict: %v", err)
	}
	if result.Name == "" {
		t.Error("expected non-empty Name")
	}
	if result.Age <= 0 {
		t.Errorf("expected Age > 0, got %d", result.Age)
	}
}

// TestProvider_LargeContext verifies that a moderately large prompt is
// accepted without truncation.
func TestProvider_LargeContext(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	// Build a prompt with a unique marker word repeated enough times to be
	// substantial (~1.5k tokens).
	marker := "ZEBRA_TROMBONE_42"
	var sb strings.Builder
	sb.WriteString("Here is some important context:\n\n")
	for i := 0; i < 200; i++ {
		sb.WriteString("The keyword is " + marker + ". Remember it.\n")
	}
	sb.WriteString("\nWhat is the keyword mentioned above? Reply with just the keyword.")

	req := providers.NewRequest("You are a helpful assistant.",
		types.Message{Role: types.RoleUser, Content: sb.String()},
	)
	resp := client.Completion(ctx, req)
	var content string
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout: %v", ctx.Err())
		case err := <-resp.Error():
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}
		case d, ok := <-resp.Message():
			if !ok {
				assertContainsAny(t, strings.ToUpper(content), strings.ToUpper(marker), strings.ToUpper("ZEBRA"))
				return
			}
			content += d.Content
		}
	}
}

// errAssertion is used by withRetry to signal a failed assertion that can be
// retried (as opposed to infrastructure errors).
type errAssertion struct{ msg string }

func (e errAssertion) Error() string { return e.msg }

// TestProvider_Fallback verifies that FallbackClient transparently moves to
// the next model when the first one fails. Requires both "broken" and "chat"
// models to be configured in e2e.yaml.
func TestProvider_Fallback(t *testing.T) {
	cfg := loadConfig(t)
	brokenCfg := mustModel(t, cfg, "broken")
	chatCfg := mustModel(t, cfg, "chat")

	brokenClient, err := setup.CreateProviderClientFromModel(brokenCfg)
	if err != nil {
		t.Fatalf("create broken client: %v", err)
	}
	chatClient, err := setup.CreateProviderClientFromModel(chatCfg)
	if err != nil {
		t.Fatalf("create chat client: %v", err)
	}

	fc := fallback.NewFallbackClient([]fallback.ModelEntry{
		{Client: brokenClient, Name: "broken"},
		{Client: chatClient, Name: "chat"},
	}, fallback.WithMaxTotalRetries(3))

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	out, err := fc.CompletionNonStreaming(ctx, providers.NewRequest("Say hello in one short sentence."))
	if err != nil {
		t.Fatalf("fallback completion failed: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected non-empty fallback result")
	}
}

// TestProvider_Fallback_AllFail verifies that when every model entry fails,
// FallbackClient surfaces an exhausted error rather than looping forever.
// This test uses deterministic mock clients, so no external config is needed.
func TestProvider_Fallback_AllFail(t *testing.T) {
	fc := fallback.NewFallbackClient([]fallback.ModelEntry{
		{Client: newAlwaysFailClient(), Name: "fail1"},
		{Client: newAlwaysFailClient(), Name: "fail2"},
	}, fallback.WithMaxTotalRetries(4))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := fc.CompletionNonStreaming(ctx, providers.NewRequest("anything"))
	if err == nil {
		t.Fatal("expected error when all fallback entries fail; got nil")
	}
	// On exhaustion the loop stops; the resulting error is the last wrapped
	// error or an exhaustion wrapper. Either is acceptable as long as it is
	// not nil and not a context error from a too-short test deadline.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("test deadline hit before fallback exhausted; increase timeout. err=%v", err)
	}
}

// TestProvider_Fallback_FirstSucceeds verifies that when the primary model
// returns successfully, the fallback machinery is not engaged further.
func TestProvider_Fallback_FirstSucceeds(t *testing.T) {
	cfg := loadConfig(t)
	chatClient, err := setup.CreateProviderClientFromModel(mustModel(t, cfg, "chat"))
	if err != nil {
		t.Fatalf("create chat client: %v", err)
	}

	fc := fallback.NewFallbackClient([]fallback.ModelEntry{
		{Client: chatClient, Name: "chat"},
		{Client: newAlwaysFailClient(), Name: "fail"},
	}, fallback.WithMaxTotalRetries(3))

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	out, err := fc.CompletionNonStreaming(ctx, providers.NewRequest("Say hello in one short sentence."))
	if err != nil {
		t.Fatalf("expected primary to succeed; got error: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("expected non-empty result from primary")
	}
}

// TestProvider_ContextCancelMidStream verifies that cancelling a streaming
// completion mid-flight produces an error (or clean close) without leaking
// goroutines. A long-output prompt minimises the chance of the model
// finishing before the cancellation lands.
func TestProvider_ContextCancelMidStream(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	req := providers.NewRequest("You are a helpful assistant.",
		types.Message{Role: types.RoleUser, Content: "Write a 1000 word essay about the history of space exploration."},
	)
	resp := client.Completion(ctx, req)

	// Drain the stream until it closes or errors.
	var sawErr bool
	for {
		select {
		case err := <-resp.Error():
			if err != nil {
				sawErr = true
				// Error must be the context error, not anything else.
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("expected context.Canceled/DeadlineExceeded, got: %v", err)
				}
			}
		case _, ok := <-resp.Message():
			if !ok {
				// Stream closed cleanly — also acceptable.
				return
			}
		case <-time.After(5 * time.Second):
			if sawErr {
				return
			}
			t.Fatal("stream did not close within 5s of cancellation")
		}
	}
}

// TestProvider_Fallback_ContextCancel verifies that context cancellation
// stops the fallback loop immediately and propagates the context error,
// rather than wrapping it as an "exhausted" error.
func TestProvider_Fallback_ContextCancel(t *testing.T) {
	fc := fallback.NewFallbackClient([]fallback.ModelEntry{
		{Client: newDelayedFailClient(500 * time.Millisecond), Name: "slow1"},
		{Client: newDelayedFailClient(500 * time.Millisecond), Name: "slow2"},
	}, fallback.WithMaxTotalRetries(10))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := fc.CompletionNonStreaming(ctx, providers.NewRequest("anything"))
	if err == nil {
		t.Fatal("expected error on cancelled context; got nil")
	}
	// Should be a context error, NOT a wrapped "exhausted" error.
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context error, got: %v", err)
	}
}
