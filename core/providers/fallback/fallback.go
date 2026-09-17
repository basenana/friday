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

// ModelEntry pairs a leaf client with the immutable routing metadata needed to
// select it and report the effective runtime state.
type ModelEntry struct {
	Client          providers.Client
	Capabilities    ModelCapabilities
	Name            string
	Key             string
	ReasoningEffort string
}

type sessionPolicyState struct {
	policy  providers.ClientPolicy
	version uint64
}

// SessionPolicy is the mutable, session-scoped routing state shared by the
// root client and every agent view derived from it.
type SessionPolicy struct {
	state atomic.Pointer[sessionPolicyState]
}

func NewSessionPolicy(initial providers.ClientPolicy) *SessionPolicy {
	p := &SessionPolicy{}
	p.Update(initial)
	return p
}

func (p *SessionPolicy) Update(next providers.ClientPolicy) {
	if p == nil {
		return
	}
	for {
		current := p.state.Load()
		version := uint64(1)
		if current != nil {
			version = current.version + 1
		}
		replacement := &sessionPolicyState{policy: next, version: version}
		if p.state.CompareAndSwap(current, replacement) {
			return
		}
	}
}

func (p *SessionPolicy) Snapshot() providers.ClientPolicy {
	if p == nil {
		return providers.ClientPolicy{}
	}
	if state := p.state.Load(); state != nil {
		return state.policy
	}
	return providers.ClientPolicy{}
}

func (p *SessionPolicy) snapshot() (providers.ClientPolicy, uint64) {
	if p == nil {
		return providers.ClientPolicy{}, 0
	}
	if state := p.state.Load(); state != nil {
		return state.policy, state.version
	}
	return providers.ClientPolicy{}, 0
}

// ModelPool owns an immutable set of leaf provider clients. Client views share
// these entries and alter only request-local order and effort.
type ModelPool struct {
	models []ModelEntry
}

func NewModelPool(entries []ModelEntry) *ModelPool {
	return &ModelPool{models: append([]ModelEntry(nil), entries...)}
}

func (p *ModelPool) NewClient(policy *SessionPolicy, defaults providers.ClientPolicy, opts ...FallbackOption) *FallbackClient {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxTotalRetries <= 0 {
		cfg.maxTotalRetries = len(p.models)
	}
	return &FallbackClient{
		pool:            p,
		sessionPolicy:   policy,
		defaults:        defaults,
		maxTotalRetries: cfg.maxTotalRetries,
		logger:          logger.New("fallback"),
	}
}

// FallbackClient tries each eligible model at most once. Each leaf provider is
// responsible for its own bounded physical-request retries.
type FallbackClient struct {
	pool            *ModelPool
	sessionPolicy   *SessionPolicy
	defaults        providers.ClientPolicy
	maxTotalRetries int
	logger          logger.Logger
	runtime         atomic.Pointer[clientRuntimeState]
	// primaryIndex points at the model currently used as the starting
	// point for new requests. Callers can advance it via Fallback() to
	// drive caller-side model rotation (e.g. when a reviewer agent
	// wants to switch models before retrying a whole review).
	primaryIndex atomic.Int32
}

type clientRuntimeState struct {
	info          providers.ClientRuntimeInfo
	policyVersion uint64
}

// NewFallbackClient creates a new FallbackClient with the given ordered model entries.
func NewFallbackClient(entries []ModelEntry, opts ...FallbackOption) *FallbackClient {
	pool := NewModelPool(entries)
	fc := pool.NewClient(NewSessionPolicy(providers.ClientPolicy{}), providers.ClientPolicy{}, opts...)

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

// Fork returns a lightweight agent view over the same immutable model pool.
// Session policy remains authoritative; defaults apply only when the session
// has no explicit value.
func (fc *FallbackClient) Fork(defaults providers.ClientPolicy) providers.Client {
	if fc == nil || fc.pool == nil {
		return fc
	}
	return fc.pool.NewClient(fc.sessionPolicy, defaults, WithMaxTotalRetries(fc.maxTotalRetries))
}

// SessionPolicy returns the mutable Session policy shared by this view and
// every view forked from it.
func (fc *FallbackClient) SessionPolicy() *SessionPolicy {
	if fc == nil {
		return nil
	}
	return fc.sessionPolicy
}

// Completion tries each candidate once in order, without cycling back.
func (fc *FallbackClient) Completion(ctx context.Context, req providers.Request) providers.Response {
	ctx, span := tracing.Start(ctx, "llm.fallback.completion")
	resp := providers.NewCommonResponse()
	policy, policyVersion := fc.requestPolicy(req)
	models := fc.candidateModels(req, policy.PreferredModel)

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
		for modelIndex := 0; modelIndex < len(models) && attempt < fc.maxTotalRetries; modelIndex++ {
			entry := models[modelIndex]
			attempt++
			modelReq := fc.prepareRequest(req, entry)
			fc.recordRuntime(entry, modelReq, policyVersion)

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
	policy, policyVersion := fc.requestPolicy(req)
	models := fc.candidateModels(req, policy.PreferredModel)
	if len(models) == 0 {
		return "", fmt.Errorf("fallback has no models configured")
	}

	var attempt int
	var lastErr error
	for modelIndex := 0; modelIndex < len(models) && attempt < fc.maxTotalRetries; modelIndex++ {
		entry := models[modelIndex]
		attempt++
		modelReq := fc.prepareRequest(req, entry)
		fc.recordRuntime(entry, modelReq, policyVersion)

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
	policy, policyVersion := fc.requestPolicy(req)
	models := fc.candidateModels(req, policy.PreferredModel)
	if len(models) == 0 {
		return fmt.Errorf("fallback has no models configured")
	}

	var attempt int
	var lastErr error
	for modelIndex := 0; modelIndex < len(models) && attempt < fc.maxTotalRetries; modelIndex++ {
		entry := models[modelIndex]
		attempt++
		modelReq := fc.prepareRequest(req, entry)
		fc.recordRuntime(entry, modelReq, policyVersion)

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
	currentEntry, nextEntry := models[currentIndex], models[next]
	providers.NotifyRetry(ctx, providers.RetryEvent{
		Provider: "fallback", Model: nextEntry.Name, ModelKey: nextEntry.Key,
		PreviousModel: currentEntry.Name, PreviousModelKey: currentEntry.Key,
		Attempt: attempts + 1, MaxAttempts: min(len(models), fc.maxTotalRetries), Error: cause,
	})
}

// ContextWindow returns the minimum context window across all models.
func (fc *FallbackClient) ContextWindow() int64 {
	var min int64 = -1
	for _, entry := range fc.pool.models {
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
	for _, entry := range fc.pool.models {
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
	models := fc.orderedModels(fc.effectivePolicy().PreferredModel)
	if len(models) == 0 {
		return ""
	}
	return models[0].Name
}

// SupportsImage returns true if any model entry supports image input.
func (fc *FallbackClient) SupportsImage() bool {
	return AnySupportsImage(fc.pool.models)
}

// Entries returns a copy of the model entries.
func (fc *FallbackClient) Entries() []ModelEntry {
	return fc.orderedModels(fc.effectivePolicy().PreferredModel)
}

// Fallback advances the internal primary model pointer to the next model
// (circular wrap-around). If the current primary supports image input, only
// entries with the same capability are considered, so image-capable rotation
// stays image-capable; if none qualifies, the plain next entry is used.
// Subsequent requests will start from the new primary. No-op if only one
// model is configured. Safe for concurrent use.
func (fc *FallbackClient) Fallback() {
	if fc == nil || fc.pool == nil || len(fc.pool.models) <= 1 {
		return
	}
	for {
		old := fc.primaryIndex.Load()
		if old < 0 || int(old) >= len(fc.pool.models) {
			old = 0
		}
		needImage := fc.pool.models[int(old)].Capabilities.SupportsImage

		next := -1
		fallback := (int(old) + 1) % len(fc.pool.models)
		for i := 1; i < len(fc.pool.models); i++ {
			candidate := (int(old) + i) % len(fc.pool.models)
			if !needImage || fc.pool.models[candidate].Capabilities.SupportsImage {
				next = candidate
				break
			}
		}
		if next < 0 {
			next = fallback
		}

		if fc.primaryIndex.CompareAndSwap(old, int32(next)) {
			fc.runtime.Store(nil)
			fc.logger.Infow("fallback client primary model advanced",
				"from_index", old, "to_index", next,
				"to_model", fc.pool.models[next].Name)
			return
		}
	}
}

// candidateModels returns the entries to try for req, in order. For image
// requests with at least one image-capable entry, image-capable entries come
// first and text-only entries are appended as a last resort (their requests
// get images stripped by prepareRequest).
func (fc *FallbackClient) candidateModels(req providers.Request, preferred string) []ModelEntry {
	models := fc.orderedModels(preferred)
	if !RequestHasImage(req) || !fc.SupportsImage() {
		return models
	}

	candidates := make([]ModelEntry, 0, len(models))
	textOnly := make([]ModelEntry, 0, len(models))
	for _, entry := range models {
		if entry.Capabilities.SupportsImage {
			candidates = append(candidates, entry)
		} else {
			textOnly = append(textOnly, entry)
		}
	}
	return append(candidates, textOnly...)
}

func (fc *FallbackClient) effectivePolicy() providers.ClientPolicy {
	policy, _ := fc.effectivePolicyState()
	return policy
}

func (fc *FallbackClient) effectivePolicyState() (providers.ClientPolicy, uint64) {
	policy, version := fc.sessionPolicy.snapshot()
	if policy.PreferredModel == "" {
		policy.PreferredModel = fc.defaults.PreferredModel
	}
	if policy.Effort == "" {
		policy.Effort = fc.defaults.Effort
	}
	if policy.PreferredModel == "" && fc.pool != nil && len(fc.pool.models) > 0 {
		idx := int(fc.primaryIndex.Load())
		if idx >= 0 && idx < len(fc.pool.models) {
			policy.PreferredModel = fc.pool.models[idx].Name
		}
	}
	return policy, version
}

func (fc *FallbackClient) requestPolicy(req providers.Request) (providers.ClientPolicy, uint64) {
	policy, version := fc.effectivePolicyState()
	// Explicit request effort remains authoritative. Plan Mode uses the
	// separate request default effort and is resolved by the selected provider.
	if providers.RequestReasoningEffort(req) == "" && policy.Effort != "" {
		providers.SetRequestReasoningEffort(req, policy.Effort)
	}
	return policy, version
}

func (fc *FallbackClient) recordRuntime(entry ModelEntry, req providers.Request, policyVersion uint64) {
	if fc == nil {
		return
	}
	fc.runtime.Store(&clientRuntimeState{
		info: providers.ClientRuntimeInfo{
			Model:       entry.Name,
			EndpointKey: entry.Key,
			Effort:      providers.ResolveReasoningEffort(req, entry.ReasoningEffort),
			Actual:      true,
		},
		policyVersion: policyVersion,
	})
}

// RuntimeInfo returns the actual candidate most recently attempted by this
// client view. After a Session policy change, the stale actual snapshot is
// ignored and the next candidate is derived from the new policy until a
// physical request is made.
func (fc *FallbackClient) RuntimeInfo() providers.ClientRuntimeInfo {
	if fc == nil {
		return providers.ClientRuntimeInfo{}
	}
	policy, version := fc.effectivePolicyState()
	if current := fc.runtime.Load(); current != nil && current.policyVersion == version {
		return current.info
	}
	models := fc.orderedModels(policy.PreferredModel)
	if len(models) == 0 {
		return providers.ClientRuntimeInfo{}
	}
	entry := models[0]
	effort := policy.Effort
	if effort == "" {
		effort = entry.ReasoningEffort
	}
	return providers.ClientRuntimeInfo{
		Model: entry.Name, EndpointKey: entry.Key, Effort: effort, Actual: false,
	}
}

func (fc *FallbackClient) orderedModels(preferred string) []ModelEntry {
	if fc == nil || fc.pool == nil {
		return nil
	}
	preferredModels := make([]ModelEntry, 0, len(fc.pool.models))
	rest := make([]ModelEntry, 0, len(fc.pool.models))
	for _, entry := range fc.pool.models {
		if preferred != "" && entry.Name == preferred {
			preferredModels = append(preferredModels, entry)
		} else {
			rest = append(rest, entry)
		}
	}
	if len(preferredModels) == 0 {
		return append([]ModelEntry(nil), fc.pool.models...)
	}
	return append(preferredModels, rest...)
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
	_ providers.Client                    = (*FallbackClient)(nil)
	_ providers.ContextWindowProvider     = (*FallbackClient)(nil)
	_ providers.ForkableClient            = (*FallbackClient)(nil)
	_ providers.ClientRuntimeInfoProvider = (*FallbackClient)(nil)
)
