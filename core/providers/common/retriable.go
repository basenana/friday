package common

import (
	"errors"
	"strings"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	openaiapi "github.com/openai/openai-go"
)

// MaxRetriableAttempts is the number of times a provider client retries a
// retriable LLM error before giving up.
const MaxRetriableAttempts = 3

// IsRetriableError reports whether err is worth retrying: rate limiting and
// transient server-side failures. It prefers typed SDK errors (exchanging the
// HTTP status code) and only falls back to tightened message matching for
// untyped errors, so digits that merely appear in request IDs or model names
// do not trigger a retry.
func IsRetriableError(err error) bool {
	if err == nil {
		return false
	}

	var anthropicErr *anthropicapi.Error
	if errors.As(err, &anthropicErr) {
		return IsRetriableStatus(anthropicErr.StatusCode)
	}
	var openaiErr *openaiapi.Error
	if errors.As(err, &openaiErr) {
		return IsRetriableStatus(openaiErr.StatusCode)
	}

	return isRetriableMessage(err.Error())
}

// IsRetriableStatus reports whether an HTTP status code is retriable:
// 429 (rate limit) and 500-529 (server errors, incl. 529 overloaded).
func IsRetriableStatus(statusCode int) bool {
	return statusCode == 429 || (statusCode >= 500 && statusCode <= 529)
}

// retriableMessages are specific phrases matched against untyped error
// strings. Bare status digits ("503") are deliberately not matched because
// they false-positive on request IDs and model names.
var retriableMessages = []string{
	"too many requests",
	"rate limit",
	"rate_limit",
	"overloaded",
	"capacity",
	"service unavailable",
	"bad gateway",
	"gateway timeout",
	"internal server error",
	"status 429",
	"status 500",
	"status 502",
	"status 503",
	"status 504",
	"status 529",
	"http 429",
	"http 500",
	"http 502",
	"http 503",
	"http 504",
	"http 529",
}

func isRetriableMessage(msg string) bool {
	lower := strings.ToLower(msg)
	for _, pattern := range retriableMessages {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
