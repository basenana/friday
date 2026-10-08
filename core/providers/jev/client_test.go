package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
)

const successResponse = `{
  "id":"gen-dec-1",
  "model":"typesafe/jev-1.13",
  "provider":"TypeSafe",
  "answers":{
    "is_bug":{"type":"noul","noul":0.96},
    "team":{"type":"choice","choice":"payments","confidence":0.75,"probabilities":{"account":0.16,"payments":0.84}},
    "urgency":{"type":"score","score":1.99,"confidence":0.99,"probabilities":{"0":0.01,"1":0.99},"legend":{"0":"later","1":"now"}}
  },
  "usage":{"input_tokens":476,"output_tokens":70,"cost":0.000019992}
}`

func validRequest() providers.DecisionRequest {
	return providers.DecisionRequest{
		State: map[string]any{"ticket": "checkout is blank"},
		Questions: map[string]providers.DecisionQuestion{
			"is_bug": providers.NoulQuestion{
				Instructions: "Is this a bug?",
				Criteria:     &providers.NoulCriteria{True: "broken behavior", False: "question"},
			},
			"team": providers.ChoiceQuestion{
				Instructions: map[string]any{"task": "Choose a team"},
				Criteria:     map[string]any{"account": "login", "payments": "billing"},
			},
			"urgency": providers.ScoreQuestion{
				Instructions: []any{"Score urgency"},
				Criteria:     []any{"later", map[string]any{"label": "now"}},
			},
		},
		SessionID: "session-1",
		User:      "user-1",
	}
}

func newTestClient(baseURL string) providers.DecisionProvider {
	return New(baseURL, "secret-key", Model{Name: "typesafe/jev-1.13", QPM: 60000})
}

func TestEvaluateEncodesProtocolAndDecodesAllAnswers(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/systemone" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successResponse))
	}))
	defer server.Close()

	response, err := newTestClient(server.URL+"/api/v1/").Evaluate(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}

	if body["model"] != "typesafe/jev-1.13" || body["session_id"] != "session-1" || body["user"] != "user-1" {
		t.Fatalf("request metadata = %#v", body)
	}
	questions := body["questions"].(map[string]any)
	if questions["is_bug"].(map[string]any)["type"] != "noul" || questions["team"].(map[string]any)["type"] != "choice" || questions["urgency"].(map[string]any)["type"] != "score" {
		t.Fatalf("questions = %#v", questions)
	}
	if response.ID != "gen-dec-1" || response.Model != "typesafe/jev-1.13" || response.Provider != "TypeSafe" {
		t.Fatalf("response metadata = %+v", response)
	}
	if got := response.Answers["is_bug"].(providers.NoulAnswer).Noul; got != 0.96 {
		t.Fatalf("noul = %v", got)
	}
	choice := response.Answers["team"].(providers.ChoiceAnswer)
	if choice.Choice != "payments" || choice.Confidence != 0.75 || choice.Probabilities["payments"] != 0.84 {
		t.Fatalf("choice = %+v", choice)
	}
	score := response.Answers["urgency"].(providers.ScoreAnswer)
	if score.Score != 1.99 || score.Confidence != 0.99 || score.Probabilities["1"] != 0.99 || score.Legend["1"] != "now" {
		t.Fatalf("score = %+v", score)
	}
	if response.Usage.InputTokens != 476 || response.Usage.OutputTokens != 70 || response.Usage.Cost != 0.000019992 {
		t.Fatalf("usage = %+v", response.Usage)
	}
}

func TestEvaluateAcceptsSupportedStateShapes(t *testing.T) {
	states := []any{"ticket", map[string]any{"ticket": "blank"}, []any{"ticket", map[string]any{"tier": "enterprise"}}}
	var received []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		received = append(received, body["state"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successResponse))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	for _, state := range states {
		req := validRequest()
		req.State = state
		if _, err := client.Evaluate(context.Background(), req); err != nil {
			t.Fatalf("state %T: %v", state, err)
		}
	}
	if !reflect.DeepEqual(received, states) {
		t.Fatalf("received states = %#v, want %#v", received, states)
	}
}

func TestEvaluateAllowsNoulWithoutCriteria(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		noul := body["questions"].(map[string]any)["is_bug"].(map[string]any)
		if _, ok := noul["criteria"]; ok {
			t.Errorf("criteria unexpectedly encoded: %#v", noul)
		}
		_, _ = w.Write([]byte(successResponse))
	}))
	defer server.Close()

	req := validRequest()
	req.Questions = map[string]providers.DecisionQuestion{"is_bug": providers.NoulQuestion{Instructions: "bug?"}}
	if _, err := newTestClient(server.URL).Evaluate(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateRejectsInvalidRequestsBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(successResponse))
	}))
	defer server.Close()

	tests := []struct {
		name   string
		model  string
		mutate func(*providers.DecisionRequest)
	}{
		{name: "empty model", mutate: func(*providers.DecisionRequest) {}},
		{name: "empty questions", model: "jev", mutate: func(r *providers.DecisionRequest) { r.Questions = nil }},
		{name: "empty key", model: "jev", mutate: func(r *providers.DecisionRequest) {
			r.Questions = map[string]providers.DecisionQuestion{"": providers.NoulQuestion{Instructions: "x"}}
		}},
		{name: "nil question", model: "jev", mutate: func(r *providers.DecisionRequest) { r.Questions = map[string]providers.DecisionQuestion{"x": nil} }},
		{name: "unsupported pointer question", model: "jev", mutate: func(r *providers.DecisionRequest) {
			r.Questions = map[string]providers.DecisionQuestion{"x": (*providers.NoulQuestion)(nil)}
		}},
		{name: "half noul criteria", model: "jev", mutate: func(r *providers.DecisionRequest) {
			r.Questions = map[string]providers.DecisionQuestion{"x": providers.NoulQuestion{Instructions: "x", Criteria: &providers.NoulCriteria{True: "yes"}}}
		}},
		{name: "empty choice criteria", model: "jev", mutate: func(r *providers.DecisionRequest) {
			r.Questions = map[string]providers.DecisionQuestion{"x": providers.ChoiceQuestion{Instructions: "x"}}
		}},
		{name: "empty score criteria", model: "jev", mutate: func(r *providers.DecisionRequest) {
			r.Questions = map[string]providers.DecisionQuestion{"x": providers.ScoreQuestion{Instructions: "x"}}
		}},
		{name: "unsupported state", model: "jev", mutate: func(r *providers.DecisionRequest) { r.State = 42 }},
		{name: "unserializable instructions", model: "jev", mutate: func(r *providers.DecisionRequest) {
			r.Questions = map[string]providers.DecisionQuestion{"x": providers.NoulQuestion{Instructions: make(chan int)}}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.mutate(&req)
			_, err := New(server.URL, "key", Model{Name: tt.model, QPM: 60000}).Evaluate(context.Background(), req)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("network calls = %d, want 0", got)
	}
}

func TestEvaluateAllowsOptionalAnswerMetadataToBeOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"choice":{"type":"choice","choice":"a"},"score":{"type":"score","score":1}},"model":"jev","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	response, err := newTestClient(server.URL).Evaluate(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	choice := response.Answers["choice"].(providers.ChoiceAnswer)
	if choice.Choice != "a" || choice.Confidence != 0 || choice.Probabilities != nil {
		t.Fatalf("choice = %+v", choice)
	}
	score := response.Answers["score"].(providers.ScoreAnswer)
	if score.Score != 1 || score.Confidence != 0 || score.Probabilities != nil || score.Legend != nil {
		t.Fatalf("score = %+v", score)
	}
}

func TestEvaluateRejectsMalformedAndInvalidAnswers(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed json", body: `{`, want: "decode response"},
		{name: "missing type", body: `{"answers":{"q":{"noul":0.5}}}`, want: `answer "q": missing type`},
		{name: "unknown type", body: `{"answers":{"q":{"type":"other"}}}`, want: `answer "q": unknown type`},
		{name: "missing noul", body: `{"answers":{"q":{"type":"noul"}}}`, want: `answer "q": missing noul`},
		{name: "missing choice", body: `{"answers":{"q":{"type":"choice"}}}`, want: `answer "q": missing choice`},
		{name: "missing score", body: `{"answers":{"q":{"type":"score"}}}`, want: `answer "q": missing score`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tt.body)) }))
			defer server.Close()
			_, err := newTestClient(server.URL).Evaluate(context.Background(), validRequest())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestEvaluateDoesNotRetryClientErrorAndRedactsSecrets(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"invalid request","metadata":{"detail":"ignored"}}}`))
	}))
	defer server.Close()

	req := validRequest()
	req.State = map[string]any{"secret": "private-state"}
	_, err := newTestClient(server.URL).Evaluate(context.Background(), req)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 || apiErr.Code != 400 || apiErr.Message != "invalid request" {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "private-state") {
		t.Fatalf("error leaked request data: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestEvaluateRetriesTransientStatusesAtMostThreeTimes(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempt := calls.Add(1)
				w.Header().Set("Retry-After", "0")
				if attempt < 3 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"code":%d,"message":"temporary"}}`, status)))
					return
				}
				_, _ = w.Write([]byte(successResponse))
			}))
			defer server.Close()

			var retries []providers.RetryEvent
			ctx := providers.WithRetryObserver(context.Background(), func(event providers.RetryEvent) { retries = append(retries, event) })
			if _, err := newTestClient(server.URL).Evaluate(ctx, validRequest()); err != nil {
				t.Fatal(err)
			}
			if got := calls.Load(); got != 3 || len(retries) != 2 || retries[0].Attempt != 2 || retries[1].Attempt != 3 {
				t.Fatalf("calls=%d retries=%#v", got, retries)
			}
			for _, retry := range retries {
				if retry.Provider != "jev" {
					t.Fatalf("retry provider = %q, want jev", retry.Provider)
				}
			}
		})
	}
}

func TestEvaluateStopsAfterThreeTransientFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"temporary"}}`))
	}))
	defer server.Close()

	_, err := newTestClient(server.URL).Evaluate(context.Background(), validRequest())
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error = %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestParseRetryAfterCapsDelayAndAcceptsHTTPDate(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if got, ok := parseRetryAfter("60", now); !ok || got != 30*time.Second {
		t.Fatalf("seconds delay = %v, %v", got, ok)
	}
	if got, ok := parseRetryAfter(now.Add(10*time.Second).Format(http.TimeFormat), now); !ok || got != 10*time.Second {
		t.Fatalf("date delay = %v, %v", got, ok)
	}
}

func TestEvaluateStopsRetryingWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
		cancel()
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"temporary"}}`))
	}))
	defer server.Close()

	_, err := newTestClient(server.URL).Evaluate(ctx, validRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestEvaluateUsesConfiguredProxy(t *testing.T) {
	var target string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target = r.URL.String()
		_, _ = w.Write([]byte(successResponse))
	}))
	defer proxy.Close()

	client := New("http://decision.invalid/api/v1/", "key", Model{Name: "jev", QPM: 60000, Proxy: proxy.URL})
	if _, err := client.Evaluate(context.Background(), validRequest()); err != nil {
		t.Fatal(err)
	}
	if target != "http://decision.invalid/api/v1/systemone" {
		t.Fatalf("proxy target = %q", target)
	}
}
