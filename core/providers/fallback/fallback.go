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
			if current.policy == next {
				return
			}
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
// these entries while keeping independent in-memory routing cursors.
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

// FallbackClient tries each eligible model at most once and remembers the last
// successful endpoint per request class. Each leaf provider is responsible for
// its own bounded physical-request retries.
type FallbackClient struct {
	pool            *ModelPool
	sessionPolicy   *SessionPolicy
	defaults        providers.ClientPolicy
	maxTotalRetries int
	logger          logger.Logger
	route           atomic.Pointer[clientRouteState]
}

type requestClass uint8

const (
	requestClassText requestClass = iota
	requestClassImage
)

type routeCursor struct {
	poolIndex int
	confirmed bool
}

// clientRouteState is immutable after publication. Text and image requests
// keep independent cursors so a stripped-image fallback cannot alter normal
// text routing. policyVersion prevents requests started under an old Session
// policy from committing stale fallback results.
type clientRouteState struct {
	policyVersion uint64
	text          routeCursor
	image         routeCursor
	lastClass     requestClass
	hasLast       bool
}

type modelCandidate struct {
	entry     ModelEntry
	poolIndex int
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
	models, class, routeSnapshot := fc.candidateModels(req, policy, policyVersion)

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
			candidate := models[modelIndex]
			entry := candidate.entry
			attempt++
			modelReq := fc.prepareRequest(req, entry)
			resp.SetRuntimeInfo(providers.ClientRuntimeInfo{
				Model:       entry.Name,
				EndpointKey: entry.Key,
				Effort:      providers.ResolveReasoningEffort(modelReq, entry.ReasoningEffort),
				Actual:      true,
			})

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
				fc.commitSuccessfulRoute(policyVersion, class, candidate.poolIndex, routeSnapshot)
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
	models, class, routeSnapshot := fc.candidateModels(req, policy, policyVersion)
	if len(models) == 0 {
		return "", fmt.Errorf("fallback has no models configured")
	}

	var attempt int
	var lastErr error
	for modelIndex := 0; modelIndex < len(models) && attempt < fc.maxTotalRetries; modelIndex++ {
		candidate := models[modelIndex]
		entry := candidate.entry
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

		fc.commitSuccessfulRoute(policyVersion, class, candidate.poolIndex, routeSnapshot)
		return result, nil
	}
	return "", fallbackExhaustedError(attempt, lastErr)
}

// StructuredPredict tries each model with circular retry for structured prediction.
func (fc *FallbackClient) StructuredPredict(ctx context.Context, req providers.Request, model any) error {
	policy, policyVersion := fc.requestPolicy(req)
	models, class, routeSnapshot := fc.candidateModels(req, policy, policyVersion)
	if len(models) == 0 {
		return fmt.Errorf("fallback has no models configured")
	}

	var attempt int
	var lastErr error
	for modelIndex := 0; modelIndex < len(models) && attempt < fc.maxTotalRetries; modelIndex++ {
		candidate := models[modelIndex]
		entry := candidate.entry
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

		fc.commitSuccessfulRoute(policyVersion, class, candidate.poolIndex, routeSnapshot)
		return nil
	}
	return fallbackExhaustedError(attempt, lastErr)
}

func (fc *FallbackClient) notifyFallback(ctx context.Context, models []modelCandidate, currentIndex, attempts int, cause error) {
	next := currentIndex + 1
	if next >= len(models) || attempts >= fc.maxTotalRetries {
		return
	}
	currentEntry, nextEntry := models[currentIndex].entry, models[next].entry
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

// ModelName returns the model selected by the client view's active route.
func (fc *FallbackClient) ModelName() string {
	return fc.RuntimeInfo().Model
}

// SupportsImage returns true if any model entry supports image input.
func (fc *FallbackClient) SupportsImage() bool {
	return AnySupportsImage(fc.pool.models)
}

// Entries returns a copy of the model entries.
func (fc *FallbackClient) Entries() []ModelEntry {
	if fc == nil {
		return nil
	}
	policy, version := fc.effectivePolicyState()
	state := fc.ensureRouteState(policy, version)
	candidates := fc.candidatesForClass(policy.PreferredModel, requestClassText)
	if state != nil {
		candidates = rotateCandidates(candidates, state.text.poolIndex)
	}
	return candidateEntries(candidates)
}

// Fallback manually advances both active routes. It preserves the historical
// behavior of preferring another image-capable entry when the current entry
// supports images. Automatic fallback promotion uses the exact successful
// endpoint instead. No-op if only one model is configured. Safe for
// concurrent use.
func (fc *FallbackClient) Fallback() {
	if fc == nil || fc.pool == nil || len(fc.pool.models) <= 1 {
		return
	}
	for {
		policy, version := fc.effectivePolicyState()
		current := fc.ensureRouteState(policy, version)
		if current == nil {
			return
		}
		old := current.text.poolIndex
		if old < 0 || old >= len(fc.pool.models) {
			old = 0
		}
		needImage := fc.pool.models[old].Capabilities.SupportsImage

		next := -1
		fallback := (old + 1) % len(fc.pool.models)
		for i := 1; i < len(fc.pool.models); i++ {
			candidate := (old + i) % len(fc.pool.models)
			if !needImage || fc.pool.models[candidate].Capabilities.SupportsImage {
				next = candidate
				break
			}
		}
		if next < 0 {
			next = fallback
		}

		replacement := *current
		replacement.text = routeCursor{poolIndex: next}
		replacement.image = routeCursor{poolIndex: next}
		replacement.lastClass = requestClassText
		replacement.hasLast = false
		if fc.route.CompareAndSwap(current, &replacement) {
			fc.logger.Infow("fallback client primary model advanced",
				"from_index", old, "to_index", next,
				"to_model", fc.pool.models[next].Name)
			return
		}
	}
}

// candidateModels returns the entries to try for req, rotated from the sticky
// cursor for that request class. Image-capable entries form the first part of
// a new image route; once a text-only fallback succeeds, rotation keeps that
// exact degraded endpoint sticky for later image requests.
func (fc *FallbackClient) candidateModels(req providers.Request, policy providers.ClientPolicy, policyVersion uint64) ([]modelCandidate, requestClass, *clientRouteState) {
	class := requestClassText
	if RequestHasImage(req) {
		class = requestClassImage
	}
	state := fc.ensureRouteState(policy, policyVersion)
	candidates := fc.candidatesForClass(policy.PreferredModel, class)
	if state == nil {
		return candidates, class, nil
	}
	cursor := state.text
	if class == requestClassImage {
		cursor = state.image
	}
	return rotateCandidates(candidates, cursor.poolIndex), class, state
}

func (fc *FallbackClient) effectivePolicyState() (providers.ClientPolicy, uint64) {
	policy, version := fc.sessionPolicy.snapshot()
	if policy.PreferredModel == "" {
		policy.PreferredModel = fc.defaults.PreferredModel
	}
	if policy.Effort == "" {
		policy.Effort = fc.defaults.Effort
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

// RuntimeInfo returns the active endpoint most recently confirmed by a
// successful request. A policy change resets the routes to their configured
// preferences with Actual=false until one succeeds.
func (fc *FallbackClient) RuntimeInfo() providers.ClientRuntimeInfo {
	if fc == nil || fc.pool == nil {
		return providers.ClientRuntimeInfo{}
	}
	policy, version := fc.effectivePolicyState()
	state := fc.ensureRouteState(policy, version)
	if state == nil {
		return providers.ClientRuntimeInfo{}
	}
	cursor := state.text
	if state.hasLast && state.lastClass == requestClassImage {
		cursor = state.image
	}
	if cursor.poolIndex < 0 || cursor.poolIndex >= len(fc.pool.models) {
		return providers.ClientRuntimeInfo{}
	}
	entry := fc.pool.models[cursor.poolIndex]
	effort := policy.Effort
	if effort == "" {
		effort = entry.ReasoningEffort
	}
	return providers.ClientRuntimeInfo{
		Model: entry.Name, EndpointKey: entry.Key, Effort: effort, Actual: cursor.confirmed,
	}
}

func (fc *FallbackClient) ensureRouteState(policy providers.ClientPolicy, policyVersion uint64) *clientRouteState {
	if fc == nil || fc.pool == nil {
		return nil
	}
	for {
		current := fc.route.Load()
		if current != nil {
			if current.policyVersion == policyVersion {
				return current
			}
			if current.policyVersion > policyVersion {
				// A request that captured an older policy may still need its
				// original ordering, but it must not publish that stale state over
				// the route already initialized for a newer generation.
				return fc.initialRouteState(policy, policyVersion)
			}
		}
		replacement := fc.initialRouteState(policy, policyVersion)
		if fc.route.CompareAndSwap(current, replacement) {
			return replacement
		}
	}
}

func (fc *FallbackClient) initialRouteState(policy providers.ClientPolicy, policyVersion uint64) *clientRouteState {
	textCandidates := fc.candidatesForClass(policy.PreferredModel, requestClassText)
	imageCandidates := fc.candidatesForClass(policy.PreferredModel, requestClassImage)
	textIndex, imageIndex := -1, -1
	if len(textCandidates) > 0 {
		textIndex = textCandidates[0].poolIndex
	}
	if len(imageCandidates) > 0 {
		imageIndex = imageCandidates[0].poolIndex
	}
	return &clientRouteState{
		policyVersion: policyVersion,
		text:          routeCursor{poolIndex: textIndex},
		image:         routeCursor{poolIndex: imageIndex},
		lastClass:     requestClassText,
	}
}

func (fc *FallbackClient) commitSuccessfulRoute(policyVersion uint64, class requestClass, poolIndex int, snapshot *clientRouteState) {
	if fc == nil || snapshot == nil || poolIndex < 0 {
		return
	}
	for {
		_, currentPolicyVersion := fc.sessionPolicy.snapshot()
		if currentPolicyVersion != policyVersion {
			return
		}
		current := fc.route.Load()
		if current == nil || current.policyVersion != policyVersion {
			return
		}
		currentCursor, snapshotCursor := current.text, snapshot.text
		if class == requestClassImage {
			currentCursor, snapshotCursor = current.image, snapshot.image
		}
		// A concurrent success for the same request class wins. Changes to the
		// other class can be merged because the cursors are independent.
		if currentCursor != snapshotCursor {
			return
		}

		replacement := *current
		cursor := routeCursor{poolIndex: poolIndex, confirmed: true}
		if class == requestClassImage {
			replacement.image = cursor
		} else {
			replacement.text = cursor
		}
		replacement.lastClass = class
		replacement.hasLast = true
		if !fc.route.CompareAndSwap(current, &replacement) {
			continue
		}
		if currentCursor.poolIndex != poolIndex {
			fc.logger.Infow("fallback client active route updated",
				"request_class", class.String(),
				"from_index", currentCursor.poolIndex,
				"to_index", poolIndex,
				"to_model", fc.pool.models[poolIndex].Name,
				"to_model_key", fc.pool.models[poolIndex].Key)
		}
		return
	}
}

func (c requestClass) String() string {
	if c == requestClassImage {
		return "image"
	}
	return "text"
}

func (fc *FallbackClient) candidatesForClass(preferred string, class requestClass) []modelCandidate {
	candidates := fc.orderedCandidates(preferred)
	if class != requestClassImage || !fc.SupportsImage() {
		return candidates
	}
	imageCapable := make([]modelCandidate, 0, len(candidates))
	textOnly := make([]modelCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.entry.Capabilities.SupportsImage {
			imageCapable = append(imageCapable, candidate)
		} else {
			textOnly = append(textOnly, candidate)
		}
	}
	return append(imageCapable, textOnly...)
}

func (fc *FallbackClient) orderedCandidates(preferred string) []modelCandidate {
	if fc == nil || fc.pool == nil {
		return nil
	}
	preferredModels := make([]modelCandidate, 0, len(fc.pool.models))
	rest := make([]modelCandidate, 0, len(fc.pool.models))
	for poolIndex, entry := range fc.pool.models {
		candidate := modelCandidate{entry: entry, poolIndex: poolIndex}
		if preferred != "" && entry.Name == preferred {
			preferredModels = append(preferredModels, candidate)
		} else {
			rest = append(rest, candidate)
		}
	}
	if len(preferredModels) == 0 {
		return rest
	}
	return append(preferredModels, rest...)
}

func rotateCandidates(candidates []modelCandidate, poolIndex int) []modelCandidate {
	if len(candidates) < 2 {
		return candidates
	}
	position := -1
	for i, candidate := range candidates {
		if candidate.poolIndex == poolIndex {
			position = i
			break
		}
	}
	if position <= 0 {
		return candidates
	}
	rotated := make([]modelCandidate, 0, len(candidates))
	rotated = append(rotated, candidates[position:]...)
	return append(rotated, candidates[:position]...)
}

func candidateEntries(candidates []modelCandidate) []ModelEntry {
	entries := make([]ModelEntry, len(candidates))
	for i, candidate := range candidates {
		entries[i] = candidate.entry
	}
	return entries
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
