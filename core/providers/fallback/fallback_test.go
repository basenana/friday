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
	name           string
	contextWindow  int64
	mu             sync.Mutex
	calls          int
	completionErrs []error // errors returned per Completion call (cycled)
	nonStreamErrs  []error
	streamContent  []string // content to stream on successful Completion
	streamEmpty    bool
	structuredErrs []error
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
	fc := NewFallbackClient([]ModelEntry{{Client: ok1, Name: "ok1"}, {Client: ok2, Name: "ok2"}})

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
	fc := NewFallbackClient([]ModelEntry{{Client: broken, Name: "broken"}, {Client: ok, Name: "ok"}}, WithMaxTotalRetries(3))

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
	fc := NewFallbackClient([]ModelEntry{{Client: b1, Name: "b1"}, {Client: b2, Name: "b2"}}, WithMaxTotalRetries(3))

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
	fc := NewFallbackClient([]ModelEntry{{Client: broken, Name: "broken"}, {Client: ok, Name: "ok"}}, WithMaxTotalRetries(3))

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
	fc := NewFallbackClient([]ModelEntry{{Client: blocking, Name: "block"}}, WithMaxTotalRetries(5))

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
	fc := NewFallbackClient([]ModelEntry{{Client: big, Name: "big"}, {Client: small, Name: "small"}})
	if got := fc.ContextWindow(); got != 50_000 {
		t.Fatalf("expected min 50000, got %d", got)
	}
}

func TestFallback_ContextWindow_DefaultWhenUnknown(t *testing.T) {
	// fakeClient with contextWindow=0 → ignored.
	c := &fakeClient{name: "zero", contextWindow: 0}
	fc := NewFallbackClient([]ModelEntry{{Client: c, Name: "zero"}})
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

func TestFallbackClientSkipsTextOnlyModelsForImageRequests(t *testing.T) {
	var textCalls, imageCalls int

	client := NewFallbackClient([]ModelEntry{
		{
			Name:         "text-only",
			Capabilities: ModelCapabilities{SupportsImage: false},
			Client: &stubCountingClient{
				nonStreaming: func(context.Context, providers.Request) (string, error) {
					textCalls++
					return "text", nil
				},
			},
		},
		{
			Name:         "vision",
			Capabilities: ModelCapabilities{SupportsImage: true},
			Client: &stubCountingClient{
				nonStreaming: func(_ context.Context, req providers.Request) (string, error) {
					imageCalls++
					if !RequestHasImage(req) {
						t.Fatal("vision request unexpectedly lost image payload")
					}
					return "vision", nil
				},
			},
		},
	}, WithMaxTotalRetries(2))

	req := providers.NewRequest("",
		types.Message{
			Role:    types.RoleUser,
			Content: "describe",
			Image: &types.ImageContent{
				Type: types.ImageTypeURL,
				URL:  "https://example.com/image.png",
			},
		},
	)

	got, err := client.CompletionNonStreaming(context.Background(), req)
	if err != nil {
		t.Fatalf("CompletionNonStreaming() error = %v", err)
	}
	if got != "vision" {
		t.Fatalf("CompletionNonStreaming() = %q, want vision", got)
	}
	if textCalls != 0 {
		t.Fatalf("text-only model calls = %d, want 0", textCalls)
	}
	if imageCalls != 1 {
		t.Fatalf("vision model calls = %d, want 1", imageCalls)
	}
}

func TestStripImagesFromRequestRemovesMultipleImages(t *testing.T) {
	req := providers.NewRequest("", types.Message{
		Role:    types.RoleUser,
		Content: "describe",
		Images: []types.ImageContent{
			{Filename: "one.png"},
			{Filename: "two.png"},
		},
	})

	stripped := StripImagesFromRequest(req)
	history := stripped.History()
	if len(history) != 1 || len(history[0].Images) != 0 || history[0].Image != nil {
		t.Fatalf("expected all images to be removed, got %#v", history)
	}
	if !strings.Contains(history[0].Content, "one.png") || !strings.Contains(history[0].Content, "two.png") {
		t.Fatalf("expected placeholders for both images, got %q", history[0].Content)
	}
}

// stubCountingClient is a minimal providers.Client that records calls.
type stubCountingClient struct {
	completion   func(context.Context, providers.Request) providers.Response
	nonStreaming func(context.Context, providers.Request) (string, error)
	structured   func(context.Context, providers.Request, any) error
}

func (s *stubCountingClient) Completion(ctx context.Context, req providers.Request) providers.Response {
	if s.completion != nil {
		return s.completion(ctx, req)
	}
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
	}()
	return resp
}

func (s *stubCountingClient) CompletionNonStreaming(ctx context.Context, req providers.Request) (string, error) {
	if s.nonStreaming != nil {
		return s.nonStreaming(ctx, req)
	}
	return "ok", nil
}

func (s *stubCountingClient) StructuredPredict(ctx context.Context, req providers.Request, model any) error {
	if s.structured != nil {
		return s.structured(ctx, req, model)
	}
	return nil
}

// Regression test: the primary model index refers to fc.models, but loops
// index the capability-filtered candidate list. A primary pointing at a
// vision entry must resolve to that entry inside the filtered list instead of
// indexing out of range or silently selecting the wrong model.
func TestFallbackPrimaryIndexResolvedWithinFilteredCandidates(t *testing.T) {
	text1 := &fakeClient{name: "text1", streamContent: []string{"text1"}}
	text2 := &fakeClient{name: "text2", streamContent: []string{"text2"}}
	vision := &fakeClient{name: "vision", streamContent: []string{"vision"}}
	fc := NewFallbackClient([]ModelEntry{
		{Client: text1, Name: "text1"},
		{Client: text2, Name: "text2"},
		{Client: vision, Name: "vision", Capabilities: ModelCapabilities{SupportsImage: true}},
	})
	// Advance the primary to index 2 (the vision model).
	fc.Fallback()
	fc.Fallback()
	if got := fc.ModelName(); got != "vision" {
		t.Fatalf("expected primary to be vision after two Fallback() calls, got %q", got)
	}

	req := providers.NewRequest("", types.Message{
		Role:    types.RoleUser,
		Content: "describe",
		Image:   &types.ImageContent{Type: types.ImageTypeURL, URL: "https://example.com/i.png"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content, err := collect(t, ctx, fc.Completion(ctx, req))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "vision" {
		t.Fatalf("expected image request to be served by the vision model, got %q", content)
	}
	if vision.callCount() != 1 {
		t.Fatalf("expected vision model called once, got %d", vision.callCount())
	}
	if text1.callCount() != 0 || text2.callCount() != 0 {
		t.Fatalf("expected text models not called, got text1=%d text2=%d", text1.callCount(), text2.callCount())
	}
}

// With only text-only models configured, an image request must be stripped
// and served instead of failing.
func TestFallbackImageRequestWithOnlyTextModelsStripsImages(t *testing.T) {
	var gotImage bool
	fc := NewFallbackClient([]ModelEntry{
		{
			Name:         "text-only",
			Capabilities: ModelCapabilities{SupportsImage: false},
			Client: &stubCountingClient{
				nonStreaming: func(_ context.Context, req providers.Request) (string, error) {
					gotImage = RequestHasImage(req)
					return "text answer", nil
				},
			},
		},
	})

	req := providers.NewRequest("", types.Message{
		Role:    types.RoleUser,
		Content: "describe",
		Image:   &types.ImageContent{Type: types.ImageTypeURL, URL: "https://example.com/i.png"},
	})

	got, err := fc.CompletionNonStreaming(context.Background(), req)
	if err != nil {
		t.Fatalf("CompletionNonStreaming() error = %v", err)
	}
	if got != "text answer" {
		t.Fatalf("CompletionNonStreaming() = %q, want stripped-text answer", got)
	}
	if gotImage {
		t.Fatal("expected images to be stripped for text-only model")
	}
}

// Image requests prefer image-capable entries, but when all of them fail the
// rotation must reach the text-only entries with images stripped.
func TestFallbackImageRequestFallsBackToStrippedTextModel(t *testing.T) {
	vision := &fakeClient{name: "vision", completionErrs: []error{errors.New("boom")}}
	var textGotImage bool
	text := &stubCountingClient{
		completion: func(_ context.Context, req providers.Request) providers.Response {
			textGotImage = RequestHasImage(req)
			resp := providers.NewCommonResponse()
			go func() {
				defer close(resp.Stream)
				defer close(resp.Err)
				resp.Stream <- providers.Delta{Content: "text fallback"}
			}()
			return resp
		},
	}
	fc := NewFallbackClient([]ModelEntry{
		{Client: vision, Name: "vision", Capabilities: ModelCapabilities{SupportsImage: true}},
		{Client: text, Name: "text", Capabilities: ModelCapabilities{SupportsImage: false}},
	}, WithMaxTotalRetries(3))

	req := providers.NewRequest("", types.Message{
		Role:    types.RoleUser,
		Content: "describe",
		Image:   &types.ImageContent{Type: types.ImageTypeURL, URL: "https://example.com/i.png"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content, err := collect(t, ctx, fc.Completion(ctx, req))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "text fallback" {
		t.Fatalf("expected stripped text model to serve the request, got %q", content)
	}
	if textGotImage {
		t.Fatal("expected images to be stripped before reaching the text-only model")
	}
	if vision.callCount() == 0 {
		t.Fatal("expected vision model to be tried first")
	}
}

// maxTokensStub extends stubCountingClient with a MaxOutputTokens budget.
type maxTokensStub struct {
	stubCountingClient
	maxOutputTokens int64
}

func (s *maxTokensStub) MaxOutputTokens() int64 { return s.maxOutputTokens }

func TestFallbackMaxOutputTokensReturnsMax(t *testing.T) {
	fc := NewFallbackClient([]ModelEntry{
		{Client: &maxTokensStub{maxOutputTokens: 4096}, Name: "small"},
		{Client: &maxTokensStub{maxOutputTokens: 16384}, Name: "large"},
		{Client: &stubCountingClient{}, Name: "unknown"},
	})
	if got := fc.MaxOutputTokens(); got != 16384 {
		t.Fatalf("expected max output tokens 16384, got %d", got)
	}
}

func TestFallbackAdvancesToModelWithSameImageCapability(t *testing.T) {
	fc := NewFallbackClient([]ModelEntry{
		{Client: &stubCountingClient{}, Name: "vision1", Capabilities: ModelCapabilities{SupportsImage: true}},
		{Client: &stubCountingClient{}, Name: "text", Capabilities: ModelCapabilities{SupportsImage: false}},
		{Client: &stubCountingClient{}, Name: "vision2", Capabilities: ModelCapabilities{SupportsImage: true}},
	})
	fc.Fallback()
	if got := fc.ModelName(); got != "vision2" {
		t.Fatalf("expected Fallback() to skip the text-only entry, got %q", got)
	}
}
