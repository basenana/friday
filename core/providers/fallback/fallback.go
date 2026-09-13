package fallback

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/tracing"
)

// ModelCapabilities describes what input modes a model supports.
type ModelCapabilities struct {
	SupportsImage bool
}

// ModelEntry pairs a providers.Client with its capabilities and a name for logging.
type ModelEntry struct {
	Client       providers.Client
	Capabilities ModelCapabilities
	Name         string
}

// FallbackClient tries each eligible model at most once. Each leaf provider is
// responsible for its own bounded physical-request retries.
type FallbackClient struct {
	models          []ModelEntry
	maxTotalRetries int
	logger          logger.Logger
	// primaryIndex points at the model currently used as the starting
	// point for new requests. Callers can advance it via Fallback() to
	// drive caller-side model rotation (e.g. when a reviewer agent
	// wants to switch models before retrying a whole review).
	primaryIndex atomic.Int32
}

// NewFallbackClient creates a new FallbackClient with the given ordered model entries.
func NewFallbackClient(entries []ModelEntry, opts ...FallbackOption) *FallbackClient {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.maxTotalRetries <= 0 {
		cfg.maxTotalRetries = len(entries)
	}

	fc := &FallbackClient{
		models:          entries,
		maxTotalRetries: cfg.maxTotalRetries,
		logger:          logger.New("fallback"),
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		imgFlag := "text-only"
		if e.Capabilities.SupportsImage {
			imgFlag = "text+image"
		}
		names[i] = fmt.Sprintf("%s(%s)", e.Name, imgFlag)
	}
	fc.logger.Infow("fallback client created", "models", names, "max_candidates", fc.maxTotalRetries)
	return fc
}

// Completion tries each candidate once in order, without cycling back.
func (fc *FallbackClient) Completion(ctx context.Context, req providers.Request) providers.Response {
	ctx, span := tracing.Start(ctx, "llm.fallback.completion")
	resp := providers.NewCommonResponse()
	models := fc.candidateModels(req)

	go func() {
		defer span.End()
		defer close(resp.Stream)
		defer close(resp.Err)

		if len(models) == 0 {
			resp.Err <- fmt.Errorf("fallback has no models configured")
			return
		}

		var (
			attempt        int
			lastErr        error
			promptTok      int64
			complTok       int64
			cachedTok      int64
			cacheCreateTok int64
		)
		start := fc.startModelIndex(req, models)
		for modelIndex := start; modelIndex < len(models) && attempt < fc.maxTotalRetries; modelIndex++ {
			entry := models[modelIndex]
			attempt++
			modelReq := fc.prepareRequest(req, entry)

			span.SetAttributes(
				tracing.String("fallback.model", entry.Name),
				tracing.IntVal("fallback.attempt", attempt),
			)

			modelResp := entry.Client.Completion(ctx, modelReq)

			// Pipe response, watching both channels through closure. Providers
			// may close their error channel before their delta channel.
			emitted := false
			fallbackRequested := false
			messageCh, errorCh := modelResp.Message(), modelResp.Error()
		PipeLoop:
			for messageCh != nil || errorCh != nil {
				select {
				case <-ctx.Done():
					resp.Err <- ctx.Err()
					return

				case err, ok := <-errorCh:
					if !ok {
						errorCh = nil
						continue
					}

					if shouldFallbackOnError(err) && !emitted {
						lastErr = err
						fc.logger.Warnw("model error, falling back",
							"model", entry.Name, "attempt", attempt, "error", err)
						fallbackRequested = true
						break PipeLoop
					}

					// Non-retriable error or already emitted deltas.
					resp.Err <- err
					return

				case delta, ok := <-messageCh:
					if !ok {
						messageCh = nil
						continue
					}
					emitted = true
					resp.Stream <- delta
				}
			}
			if !fallbackRequested {
				t := modelResp.Tokens()
				promptTok += t.PromptTokens
				complTok += t.CompletionTokens
				cachedTok += t.CachedPromptTokens
				cacheCreateTok += t.CacheCreationTokens
			}

			// If we got here with no error and emitted deltas, we're done.
			if emitted && !fallbackRequested {
				resp.SetTokens(providers.Tokens{
					PromptTokens:        promptTok,
					CompletionTokens:    complTok,
					CachedPromptTokens:  cachedTok,
					CacheCreationTokens: cacheCreateTok,
					TotalTokens:         promptTok + complTok,
				})
				return
			}

			// Discard token usage from the abandoned attempt so it is not
			// double-counted when the next model succeeds.
			promptTok, complTok, cachedTok, cacheCreateTok = 0, 0, 0, 0

			if fallbackRequested {
				fc.notifyFallback(ctx, models, modelIndex, attempt, lastErr)
				continue
			}

			// If lastErr is nil and no deltas, model returned empty — try next.
			lastErr = fmt.Errorf("model %s returned an empty response", entry.Name)
			fc.notifyFallback(ctx, models, modelIndex, attempt, lastErr)
		}
		resp.Err <- fallbackExhaustedError(attempt, lastErr)
	}()

	return resp
}

// CompletionNonStreaming tries each candidate once without cycling.
func (fc *FallbackClient) CompletionNonStreaming(ctx context.Context, req providers.Request) (string, error) {
	models := fc.candidateModels(req)
	if len(models) == 0 {
		return "", fmt.Errorf("fallback has no models configured")
	}

	var attempt int
	var lastErr error
	start := fc.startModelIndex(req, models)
	for modelIndex := start; modelIndex < len(models) && attempt < fc.maxTotalRetries; modelIndex++ {
		entry := models[modelIndex]
		attempt++
		modelReq := fc.prepareRequest(req, entry)

		result, err := entry.Client.CompletionNonStreaming(ctx, modelReq)
		if err != nil {
			if shouldFallbackOnError(err) {
				lastErr = err
				fc.logger.Warnw("non-streaming model error, falling back",
					"model", entry.Name, "attempt", attempt, "error", err)
				fc.notifyFallback(ctx, models, modelIndex, attempt, lastErr)
				continue
			}
			return "", err
		}

		return result, nil
	}
	return "", fallbackExhaustedError(attempt, lastErr)
}

// StructuredPredict tries each model with circular retry for structured prediction.
func (fc *FallbackClient) StructuredPredict(ctx context.Context, req providers.Request, model any) error {
	models := fc.candidateModels(req)
	if len(models) == 0 {
		return fmt.Errorf("fallback has no models configured")
	}

	var attempt int
	var lastErr error
	start := fc.startModelIndex(req, models)
	for modelIndex := start; modelIndex < len(models) && attempt < fc.maxTotalRetries; modelIndex++ {
		entry := models[modelIndex]
		attempt++
		modelReq := fc.prepareRequest(req, entry)

		err := entry.Client.StructuredPredict(ctx, modelReq, model)
		if err != nil {
			if shouldFallbackOnError(err) {
				lastErr = err
				fc.logger.Warnw("structured predict model error, falling back",
					"model", entry.Name, "attempt", attempt, "error", err)
				fc.notifyFallback(ctx, models, modelIndex, attempt, lastErr)
				continue
			}
			return err
		}

		return nil
	}
	return fallbackExhaustedError(attempt, lastErr)
}

func (fc *FallbackClient) notifyFallback(ctx context.Context, models []ModelEntry, currentIndex, attempts int, cause error) {
	next := currentIndex + 1
	if next >= len(models) || attempts >= fc.maxTotalRetries {
		return
	}
	providers.NotifyRetry(ctx, providers.RetryEvent{
		Provider: "fallback", Model: models[next].Name, Attempt: attempts + 1,
		MaxAttempts: min(len(models), fc.maxTotalRetries), Error: cause,
	})
}

// ContextWindow returns the minimum context window across all models.
func (fc *FallbackClient) ContextWindow() int64 {
	var min int64 = -1
	for _, entry := range fc.models {
		if cw, ok := entry.Client.(providers.ContextWindowProvider); ok {
			w := cw.ContextWindow()
			if w > 0 && (min < 0 || w < min) {
				min = w
			}
		}
	}
	if min < 0 {
		return 128 * 1000 // default 128K
	}
	return min
}

// MaxOutputTokens returns the maximum output token budget across all models,
// mirroring the min used for ContextWindow: any entry can serve the request,
// so the largest budget is available to callers' budget math. Entries that do
// not expose the capability, or report 0, are ignored. Returns 0 when no
// entry reports a budget.
func (fc *FallbackClient) MaxOutputTokens() int64 {
	var max int64
	for _, entry := range fc.models {
		if mp, ok := entry.Client.(providers.MaxOutputTokensProvider); ok {
			if v := mp.MaxOutputTokens(); v > max {
				max = v
			}
		}
	}
	return max
}

// ModelName returns the name of the current primary model entry.
// Implements providers.ModelNameProvider. Note: on fallback rotation
// mid-call the actual serving model may differ; this reflects the
// caller-visible primary.
func (fc *FallbackClient) ModelName() string {
	idx := int(fc.primaryIndex.Load())
	if idx < 0 || idx >= len(fc.models) {
		return ""
	}
	return fc.models[idx].Name
}

// SupportsImage returns true if any model entry supports image input.
func (fc *FallbackClient) SupportsImage() bool {
	return AnySupportsImage(fc.models)
}

// Entries returns a copy of the model entries.
func (fc *FallbackClient) Entries() []ModelEntry {
	result := make([]ModelEntry, len(fc.models))
	copy(result, fc.models)
	return result
}

// Fallback advances the internal primary model pointer to the next model
// (circular wrap-around). If the current primary supports image input, only
// entries with the same capability are considered, so image-capable rotation
// stays image-capable; if none qualifies, the plain next entry is used.
// Subsequent requests will start from the new primary. No-op if only one
// model is configured. Safe for concurrent use.
func (fc *FallbackClient) Fallback() {
	if len(fc.models) <= 1 {
		return
	}
	for {
		old := fc.primaryIndex.Load()
		needImage := fc.models[int(old)].Capabilities.SupportsImage

		next := -1
		fallback := (int(old) + 1) % len(fc.models)
		for i := 1; i < len(fc.models); i++ {
			candidate := (int(old) + i) % len(fc.models)
			if !needImage || fc.models[candidate].Capabilities.SupportsImage {
				next = candidate
				break
			}
		}
		if next < 0 {
			next = fallback
		}

		if fc.primaryIndex.CompareAndSwap(old, int32(next)) {
			fc.logger.Infow("fallback client primary model advanced",
				"from_index", old, "to_index", next,
				"to_model", fc.models[next].Name)
			return
		}
	}
}

// candidateModels returns the entries to try for req, in order. For image
// requests with at least one image-capable entry, image-capable entries come
// first and text-only entries are appended as a last resort (their requests
// get images stripped by prepareRequest).
func (fc *FallbackClient) candidateModels(req providers.Request) []ModelEntry {
	if !RequestHasImage(req) || !fc.SupportsImage() {
		return fc.models
	}

	candidates := make([]ModelEntry, 0, len(fc.models))
	textOnly := make([]ModelEntry, 0, len(fc.models))
	for _, entry := range fc.models {
		if entry.Capabilities.SupportsImage {
			candidates = append(candidates, entry)
		} else {
			textOnly = append(textOnly, entry)
		}
	}
	return append(candidates, textOnly...)
}

// startModelIndex resolves the caller-selected primary model (see Fallback())
// to an index within models, which may be a capability-filtered reorder of
// fc.models. Matching is by entry identity; if the primary is not part of the
// candidate list, the first candidate is used. For image requests the
// image-capable entries form the head of models, and a text-only primary
// never starts inside the text-only tail: rotation reaches it only after the
// image-capable entries are exhausted. The result is always in range.
func (fc *FallbackClient) startModelIndex(req providers.Request, models []ModelEntry) int {
	if len(models) == 0 {
		return 0
	}
	idx := int(fc.primaryIndex.Load())
	if idx < 0 || idx >= len(fc.models) {
		return 0
	}
	primary := fc.models[idx]
	if RequestHasImage(req) && !primary.Capabilities.SupportsImage && models[0].Capabilities.SupportsImage {
		return 0
	}
	for i, entry := range models {
		if entry.Client == primary.Client && entry.Name == primary.Name {
			return i
		}
	}
	return 0
}

// prepareRequest strips image content from the request if the target model
// doesn't support images.
func (fc *FallbackClient) prepareRequest(req providers.Request, entry ModelEntry) providers.Request {
	if RequestHasImage(req) && !entry.Capabilities.SupportsImage {
		fc.logger.Infow("stripping images from request for text-only model", "model", entry.Name)
		return StripImagesFromRequest(req)
	}
	return req
}

func fallbackExhaustedError(attempt int, lastErr error) error {
	if lastErr == nil {
		return fmt.Errorf("fallback exhausted after %d attempts", attempt)
	}
	return fmt.Errorf("fallback exhausted after %d attempts, last error: %w", attempt, lastErr)
}

// Ensure FallbackClient implements providers.Client and
// providers.ContextWindowProvider.
var (
	_ providers.Client                = (*FallbackClient)(nil)
	_ providers.ContextWindowProvider = (*FallbackClient)(nil)
)
