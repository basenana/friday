package common

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	openaiapi "github.com/openai/openai-go"
)

// MaxAttempts is the total number of physical requests a provider may make,
// including the initial request.
const MaxAttempts = 3

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

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}

	return isRetriableMessage(err.Error())
}

// IsRetriableStatus reports whether an HTTP status code is retriable:
// 408/409, 429, and 5xx server errors.
func IsRetriableStatus(statusCode int) bool {
	return statusCode == 408 || statusCode == 409 || statusCode == 429 || (statusCode >= 500 && statusCode <= 599)
}

// RetryAfter returns a server-requested delay, capped at 30 seconds.
func RetryAfter(err error, now time.Time) (time.Duration, bool) {
	var response *http.Response
	var anthropicErr *anthropicapi.Error
	if errors.As(err, &anthropicErr) {
		response = anthropicErr.Response
	}
	var openaiErr *openaiapi.Error
	if response == nil && errors.As(err, &openaiErr) {
		response = openaiErr.Response
	}
	if response == nil {
		return 0, false
	}
	raw := strings.TrimSpace(response.Header.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	var delay time.Duration
	if seconds, parseErr := strconv.Atoi(raw); parseErr == nil {
		delay = time.Duration(seconds) * time.Second
	} else if retryAt, parseErr := http.ParseTime(raw); parseErr == nil {
		delay = retryAt.Sub(now)
	} else {
		return 0, false
	}
	if delay < 0 {
		delay = 0
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay, true
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
	"request timeout",
	"connection reset",
	"connection refused",
	"unexpected eof",
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
