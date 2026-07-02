package fallback

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/common"
	"github.com/basenana/friday/core/tracing"
)

// ModelEntry pairs a providers.Client with a name for logging.
type ModelEntry struct {
	Client providers.Client
	Name   string
}

// FallbackClient implements providers.Client. On retriable error from model N,
// it advances to model N+1 (circular wrap-around). It retries up to
// maxTotalRetries across all models before giving up.
type FallbackClient struct {
	models          []ModelEntry
	maxTotalRetries int
	logger          logger.Logger
}

// NewFallbackClient creates a new FallbackClient with the given ordered model entries.
func NewFallbackClient(entries []ModelEntry, opts ...FallbackOption) *FallbackClient {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.maxTotalRetries <= 0 {
		cfg.maxTotalRetries = len(entries) * 3
	}

	fc := &FallbackClient{
		models:          entries,
		maxTotalRetries: cfg.maxTotalRetries,
		logger:          logger.New("fallback"),
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	slog.Info("fallback client created", "models", names, "max_retries", fc.maxTotalRetries)
	return fc
}

// Completion tries each model in order with circular retry on retriable errors.
func (fc *FallbackClient) Completion(ctx context.Context, req providers.Request) providers.Response {
	ctx, span := tracing.Start(ctx, "llm.fallback.completion")
	resp := providers.NewCommonResponse()

	go func() {
		defer span.End()
		defer close(resp.Stream)
		defer close(resp.Err)

		if len(fc.models) == 0 {
			resp.Err <- fmt.Errorf("fallback has no models configured")
			return
		}

		var (
			attempt    int
			modelIndex int
			lastErr    error
			promptTok  int64
			complTok   int64
			cachedTok  int64
		)

		for {
			if attempt >= fc.maxTotalRetries {
				resp.Err <- fallbackExhaustedError(attempt, lastErr)
				return
			}

			entry := fc.models[modelIndex]

			span.SetAttributes(
				tracing.String("fallback.model", entry.Name),
				tracing.IntVal("fallback.attempt", attempt),
			)

			modelResp := entry.Client.Completion(ctx, req)

			// Pipe response, watching for errors.
			emitted := false
			fallbackRequested := false
		PipeLoop:
			for {
				select {
				case <-ctx.Done():
					resp.Err <- ctx.Err()
					return

				case err, ok := <-modelResp.Error():
					if !ok {
						// Stream completed successfully (error channel closed with nil).
						t := modelResp.Tokens()
						promptTok += t.PromptTokens
						complTok += t.CompletionTokens
						cachedTok += t.CachedPromptTokens
						break PipeLoop
					}

					if shouldFallbackOnError(err) && !emitted {
						lastErr = err
						fc.logger.Warnw("model error, falling back",
							"model", entry.Name, "attempt", attempt, "error", err)
						attempt++
						modelIndex = (modelIndex + 1) % len(fc.models)
						fallbackRequested = true
						backoff := time.Second * time.Duration(attempt)
						if waitErr := common.WaitBackoff(ctx, backoff); waitErr != nil {
							resp.Err <- waitErr
							return
						}
						break PipeLoop // retry outer loop
					}

					// Non-retriable error or already emitted deltas.
					resp.Err <- err
					return

				case delta, ok := <-modelResp.Message():
					if !ok {
						// Stream finished normally.
						t := modelResp.Tokens()
						promptTok += t.PromptTokens
						complTok += t.CompletionTokens
						cachedTok += t.CachedPromptTokens
						break PipeLoop
					}
					emitted = true
					resp.Stream <- delta
				}
			}

			// If we got here with no error and emitted deltas, we're done.
			if emitted && !fallbackRequested {
				resp.Token = providers.Tokens{
					PromptTokens:       promptTok,
					CompletionTokens:   complTok,
					CachedPromptTokens: cachedTok,
					TotalTokens:        promptTok + complTok,
				}
				return
			}

			if fallbackRequested {
				continue
			}

			// If lastErr is nil and no deltas, model returned empty — try next.
			lastErr = nil
			attempt++
			modelIndex = (modelIndex + 1) % len(fc.models)
		}
	}()

	return resp
}

// CompletionNonStreaming tries each model with circular retry for non-streaming completion.
func (fc *FallbackClient) CompletionNonStreaming(ctx context.Context, req providers.Request) (string, error) {
	if len(fc.models) == 0 {
		return "", fmt.Errorf("fallback has no models configured")
	}

	var (
		attempt    int
		modelIndex int
		lastErr    error
	)

	for {
		if attempt >= fc.maxTotalRetries {
			return "", fallbackExhaustedError(attempt, lastErr)
		}

		entry := fc.models[modelIndex]

		result, err := entry.Client.CompletionNonStreaming(ctx, req)
		if err != nil {
			if shouldFallbackOnError(err) {
				lastErr = err
				fc.logger.Warnw("non-streaming model error, falling back",
					"model", entry.Name, "attempt", attempt, "error", err)
				attempt++
				modelIndex = (modelIndex + 1) % len(fc.models)
				backoff := time.Second * time.Duration(attempt)
				if waitErr := common.WaitBackoff(ctx, backoff); waitErr != nil {
					return "", waitErr
				}
				continue
			}
			return "", err
		}

		return result, nil
	}
}

// StructuredPredict tries each model with circular retry for structured prediction.
func (fc *FallbackClient) StructuredPredict(ctx context.Context, req providers.Request, model any) error {
	if len(fc.models) == 0 {
		return fmt.Errorf("fallback has no models configured")
	}

	var (
		attempt    int
		modelIndex int
		lastErr    error
	)

	for {
		if attempt >= fc.maxTotalRetries {
			return fallbackExhaustedError(attempt, lastErr)
		}

		entry := fc.models[modelIndex]

		err := entry.Client.StructuredPredict(ctx, req, model)
		if err != nil {
			if shouldFallbackOnError(err) {
				lastErr = err
				fc.logger.Warnw("structured predict model error, falling back",
					"model", entry.Name, "attempt", attempt, "error", err)
				attempt++
				modelIndex = (modelIndex + 1) % len(fc.models)
				backoff := time.Second * time.Duration(attempt)
				if waitErr := common.WaitBackoff(ctx, backoff); waitErr != nil {
					return waitErr
				}
				continue
			}
			return err
		}

		return nil
	}
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

func fallbackExhaustedError(attempt int, lastErr error) error {
	if lastErr == nil {
		return fmt.Errorf("fallback exhausted after %d attempts", attempt)
	}
	return fmt.Errorf("fallback exhausted after %d attempts, last error: %w", attempt, lastErr)
}

// Ensure FallbackClient implements providers.Client and providers.ContextWindowProvider.
var (
	_ providers.Client                = (*FallbackClient)(nil)
	_ providers.ContextWindowProvider = (*FallbackClient)(nil)
)
