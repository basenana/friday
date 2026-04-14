package common

import (
	"context"
	"errors"
	"testing"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
)

type structuredPredictModel struct {
	Value string `json:"value"`
}

func TestStructuredPredictWithFallbackUsesNonStreamingResultFirst(t *testing.T) {
	var streamCalls int
	var model structuredPredictModel

	err := StructuredPredictWithFallback(
		context.Background(),
		providers.NewPromptRequest("return json"),
		&model,
		func(context.Context, providers.Request) (string, error) {
			return `{"value":"non-stream"}`, nil
		},
		func(context.Context, providers.Request) providers.Response {
			streamCalls++
			return nil
		},
		logger.New("test"),
	)
	if err != nil {
		t.Fatalf("expected non-streaming structured predict to succeed, got %v", err)
	}
	if model.Value != "non-stream" {
		t.Fatalf("expected model to be filled from non-streaming response, got %#v", model)
	}
	if streamCalls != 0 {
		t.Fatalf("expected no streaming fallback call, got %d", streamCalls)
	}
}

func TestStructuredPredictWithFallbackFallsBackToStreamingOnNonStreamingError(t *testing.T) {
	var model structuredPredictModel

	err := StructuredPredictWithFallback(
		context.Background(),
		providers.NewPromptRequest("return json"),
		&model,
		func(context.Context, providers.Request) (string, error) {
			return "", errors.New("provider does not support non-stream")
		},
		func(context.Context, providers.Request) providers.Response {
			resp := providers.NewCommonResponse()
			go func() {
				defer close(resp.Stream)
				defer close(resp.Err)
				resp.Stream <- providers.Delta{Content: `{"value":"stream"}`}
			}()
			return resp
		},
		logger.New("test"),
	)
	if err != nil {
		t.Fatalf("expected streaming fallback to succeed, got %v", err)
	}
	if model.Value != "stream" {
		t.Fatalf("expected model to be filled from streaming fallback, got %#v", model)
	}
}
