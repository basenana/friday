package fallback

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

// fakeClient is a controllable providers.Client for fallback tests.
type fakeClient struct {
	name            string
	contextWindow   int64
	mu              sync.Mutex
	calls           int
	completionErrs  []error // errors returned per Completion call (cycled)
	nonStreamErrs   []error
	streamContent   []string // content to stream on successful Completion
	streamEmpty     bool
	structuredErrs  []error
}

func (f *fakeClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeClient) Completion(ctx context.Context, _ providers.Request) providers.Response {
	f.mu.Lock()
	idx := f.calls
	f.calls++
	f.mu.Unlock()

	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		if idx < len(f.completionErrs) && f.completionErrs[idx] != nil {
			resp.Err <- f.completionErrs[idx]
			return
		}
		if f.streamEmpty {
			return
		}
		for _, s := range f.streamContent {
			select {
			case <-ctx.Done():
				return
			case resp.Stream <- providers.Delta{Content: s}:
			}
		}
	}()
	return resp
}

func (f *fakeClient) CompletionNonStreaming(_ context.Context, _ providers.Request) (string, error) {
	f.mu.Lock()
	idx := f.calls
	f.calls++
	f.mu.Unlock()
	if idx < len(f.nonStreamErrs) && f.nonStreamErrs[idx] != nil {
		return "", f.nonStreamErrs[idx]
	}
	return "ok", nil
}

func (f *fakeClient) StructuredPredict(_ context.Context, _ providers.Request, _ any) error {
	f.mu.Lock()
	idx := f.calls
	f.calls++
	f.mu.Unlock()
	if idx < len(f.structuredErrs) && f.structuredErrs[idx] != nil {
		return f.structuredErrs[idx]
	}
	return nil
}

func (f *fakeClient) ContextWindow() int64 { return f.contextWindow }

func collect(t *testing.T, ctx context.Context, resp providers.Response) (string, error) {
	t.Helper()
	var content string
	for {
		select {
		case <-ctx.Done():
			return content, ctx.Err()
		case err, ok := <-resp.Error():
			if !ok {
				return content, nil
			}
			return content, err
		case delta, ok := <-resp.Message():
			if !ok {
				return content, nil
			}
			content += delta.Content
		}
	}
}

func TestFallback_FirstModelSucceeds(t *testing.T) {
	ok1 := &fakeClient{name: "ok1", streamContent: []string{"hello"}, contextWindow: 100_000}
	ok2 := &fakeClient{name: "ok2", streamContent: []string{"world"}, contextWindow: 100_000}
	fc := NewFallbackClient([]ModelEntry{{ok1, "ok1"}, {ok2, "ok2"}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	content, err := collect(t, ctx, fc.Completion(ctx, providers.NewRequest("sys")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "hello" {
		t.Fatalf("expected hello from first model, got %q", content)
	}
	if ok1.callCount() != 1 {
		t.Fatalf("expected first model called once, got %d", ok1.callCount())
	}
	if ok2.callCount() != 0 {
		t.Fatalf("expected second model not called, got %d", ok2.callCount())
	}
}

func TestFallback_FallsToSecondModel(t *testing.T) {
	broken := &fakeClient{name: "broken", completionErrs: []error{errors.New("connection refused")}}
	ok := &fakeClient{name: "ok", streamContent: []string{"recovered"}, contextWindow: 50_000}
	// Reduce backoff: use WithMaxTotalRetries so we don't iterate too many times.
	fc := NewFallbackClient([]ModelEntry{{broken, "broken"}, {ok, "ok"}}, WithMaxTotalRetries(3))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content, err := collect(t, ctx, fc.Completion(ctx, providers.NewRequest("sys")))
	if err != nil {
		t.Fatalf("expected fallback success, got error: %v", err)
	}
	if content != "recovered" {
		t.Fatalf("expected content from second model, got %q", content)
	}
}

func TestFallback_AllExhausted(t *testing.T) {
	b1 := &fakeClient{name: "b1", completionErrs: []error{errors.New("e1"), errors.New("e1"), errors.New("e1")}}
	b2 := &fakeClient{name: "b2", completionErrs: []error{errors.New("e2"), errors.New("e2"), errors.New("e2")}}
	fc := NewFallbackClient([]ModelEntry{{b1, "b1"}, {b2, "b2"}}, WithMaxTotalRetries(3))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := collect(t, ctx, fc.Completion(ctx, providers.NewRequest("sys")))
	if err == nil {
		t.Fatalf("expected exhaustion error, got nil")
	}
	if !strings.Contains(err.Error(), "fallback exhausted") {
		t.Fatalf("expected exhaustion message, got: %v", err)
	}
}

func TestFallback_NonStreaming(t *testing.T) {
	broken := &fakeClient{name: "broken", nonStreamErrs: []error{errors.New("nope")}}
	ok := &fakeClient{name: "ok"}
	fc := NewFallbackClient([]ModelEntry{{broken, "broken"}, {ok, "ok"}}, WithMaxTotalRetries(3))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := fc.CompletionNonStreaming(ctx, providers.NewRequest("sys"))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if out != "ok" {
		t.Fatalf("expected ok, got %q", out)
	}
}

func TestFallback_ContextCancelled(t *testing.T) {
	// Model that blocks until ctx cancelled.
	blocking := &fakeClient{name: "block"}
	// Fake blocks on select with ctx.Done inside the stream goroutine.
	fc := NewFallbackClient([]ModelEntry{{blocking, "block"}}, WithMaxTotalRetries(5))

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel almost immediately.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := collect(t, ctx, fc.Completion(ctx, providers.NewRequest("sys")))
	if err == nil {
		t.Fatalf("expected ctx cancellation to surface")
	}
}

func TestFallback_ContextWindow_ReturnsMin(t *testing.T) {
	big := &fakeClient{name: "big", contextWindow: 200_000}
	small := &fakeClient{name: "small", contextWindow: 50_000}
	fc := NewFallbackClient([]ModelEntry{{big, "big"}, {small, "small"}})
	if got := fc.ContextWindow(); got != 50_000 {
		t.Fatalf("expected min 50000, got %d", got)
	}
}

func TestFallback_ContextWindow_DefaultWhenUnknown(t *testing.T) {
	// fakeClient with contextWindow=0 → ignored.
	c := &fakeClient{name: "zero", contextWindow: 0}
	fc := NewFallbackClient([]ModelEntry{{c, "zero"}})
	if got := fc.ContextWindow(); got != 128_000 {
		t.Fatalf("expected default 128000, got %d", got)
	}
}

// Ensure fakeClient satisfies the interfaces we rely on.
var (
	_ providers.Client                = (*fakeClient)(nil)
	_ providers.ContextWindowProvider = (*fakeClient)(nil)
)

// silence unused import for types when fakeClient stays minimal
var _ = types.RoleUser
