package common

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
)

func StructuredPredictWithFallback(
	ctx context.Context,
	req providers.Request,
	model any,
	nonStreaming func(context.Context, providers.Request) (string, error),
	streaming func(context.Context, providers.Request) providers.Response,
	log logger.Logger,
) error {
	raw, err := nonStreaming(ctx, req)
	if err == nil {
		if err = ExtractJSON(raw, model); err == nil {
			return nil
		}
	}

	firstErr := err
	if firstErr == nil {
		firstErr = ExtractJSON(raw, model)
	}
	if log != nil {
		log.Warnw("structured predict non-streaming failed, falling back to streaming", "err", firstErr)
	}

	raw, err = ReadAllContent(ctx, streaming(ctx, req))
	if err != nil {
		return fmt.Errorf("structured predict fallback failed after non-streaming error: %w: %w", firstErr, err)
	}
	if err = ExtractJSON(raw, model); err != nil {
		return fmt.Errorf("structured predict fallback returned invalid json after non-streaming error: %w: %w", firstErr, err)
	}
	return nil
}

func ReadAllContent(ctx context.Context, resp providers.Response) (string, error) {
	if resp == nil {
		return "", fmt.Errorf("response is nil")
	}

	msgCh := resp.Message()
	errCh := resp.Error()
	var raw strings.Builder

	for msgCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil {
				return "", err
			}
		case delta, ok := <-msgCh:
			if !ok {
				msgCh = nil
				continue
			}
			if delta.Content != "" {
				raw.WriteString(delta.Content)
			}
		}
	}

	return raw.String(), nil
}
