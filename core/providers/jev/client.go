package openrouterdecision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/common"
	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

type Model struct {
	Name  string
	QPM   int64
	Proxy string
}

type client struct {
	baseURL string
	apiKey  string
	model   Model
	http    *http.Client
	limiter *rate.Limiter
}

// Error is a sanitized OpenRouter API error.
type Error struct {
	StatusCode int
	Code       int
	Message    string
	retryAfter string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("openrouter decision request failed: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("openrouter decision request failed: HTTP %d, code %d: %s", e.StatusCode, e.Code, e.Message)
}

func New(baseURL, apiKey string, model Model) providers.DecisionProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if model.Proxy != "" {
		transport.Proxy = nil
		if proxyURL, err := url.Parse(model.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	if model.QPM <= 0 {
		model.QPM = 20
	}
	burst := int(model.QPM / 2)
	if burst < 1 {
		burst = 1
	}
	return &client{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		http: &http.Client{
			Transport: transport,
			Timeout:   15 * time.Minute,
		},
		limiter: rate.NewLimiter(rate.Limit(float64(model.QPM)/60), burst),
	}
}

func (c *client) Evaluate(ctx context.Context, request providers.DecisionRequest) (providers.DecisionResponse, error) {
	body, err := encodeRequest(c.model.Name, request)
	if err != nil {
		return providers.DecisionResponse{}, err
	}

	for attempt := 1; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return providers.DecisionResponse{}, err
		}
		response, err := c.doRequest(ctx, body)
		if err == nil {
			return response, nil
		}
		if ctx.Err() != nil {
			return providers.DecisionResponse{}, ctx.Err()
		}
		if attempt >= common.MaxAttempts || !retriable(err) {
			return providers.DecisionResponse{}, err
		}

		delay := retryDelay(err, attempt)
		providers.NotifyRetry(ctx, providers.RetryEvent{
			Provider:    "openrouter",
			Model:       c.model.Name,
			Attempt:     attempt + 1,
			MaxAttempts: common.MaxAttempts,
			Error:       err,
			Backoff:     delay,
		})
		if err := common.WaitBackoff(ctx, delay); err != nil {
			return providers.DecisionResponse{}, err
		}
	}
}

func (c *client) doRequest(ctx context.Context, body []byte) (providers.DecisionResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/systemone", bytes.NewReader(body))
	if err != nil {
		return providers.DecisionResponse{}, fmt.Errorf("create OpenRouter decision request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(req)
	if err != nil {
		return providers.DecisionResponse{}, fmt.Errorf("send OpenRouter decision request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providers.DecisionResponse{}, decodeAPIError(response)
	}
	var wire wireResponse
	if err := json.NewDecoder(response.Body).Decode(&wire); err != nil {
		return providers.DecisionResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return decodeResponse(wire)
}

func decodeAPIError(response *http.Response) error {
	var payload struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload)
	return &Error{
		StatusCode: response.StatusCode,
		Code:       payload.Error.Code,
		Message:    payload.Error.Message,
		retryAfter: response.Header.Get("Retry-After"),
	}
}

func retriable(err error) bool {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return common.IsRetriableStatus(apiErr.StatusCode)
	}
	return common.IsRetriableError(err)
}

func retryDelay(err error, retry int) time.Duration {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		if delay, ok := parseRetryAfter(apiErr.retryAfter, time.Now()); ok {
			return delay
		}
	}
	return common.RetryBackoffDelay(retry)
}

func parseRetryAfter(raw string, now time.Time) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	var delay time.Duration
	if seconds, err := strconv.Atoi(raw); err == nil {
		delay = time.Duration(seconds) * time.Second
	} else if retryAt, err := http.ParseTime(raw); err == nil {
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

var _ providers.DecisionProvider = (*client)(nil)
