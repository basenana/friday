package common

import (
	"github.com/basenana/friday/core/types"
)

// TokenEstimateFactor is the empirical factor for estimating tokens from rune count
const TokenEstimateFactor = 0.6

// ApplyTokenFallback fills in token counts using FuzzyTokens if API didn't return them.
// API returned values take precedence, only applies fallback if missing.
// promptTokens: tokens from API response (0 if not returned)
// completionTokens: tokens from API response (0 if not returned)
// accumulatedContent: the accumulated content for completion token estimation
// requestMessages: the request messages for prompt token estimation
func ApplyTokenFallback(promptTokens, completionTokens int64, accumulatedContent string, requestMessages []types.Message) (promptResult, completionResult, totalResult int64) {
	// Only estimate prompt tokens if API didn't return them
	if promptTokens == 0 {
		for _, msg := range requestMessages {
			promptTokens += msg.FuzzyTokens()
		}
	}

	// Only estimate completion tokens if API didn't return them
	if completionTokens == 0 {
		completionTokens = int64(float64(len([]rune(accumulatedContent))) * TokenEstimateFactor)
	}

	return promptTokens, completionTokens, promptTokens + completionTokens
}
