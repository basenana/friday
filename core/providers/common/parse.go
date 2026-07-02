package common

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// ParseToolUseArguments strictly parses raw as a JSON object.
// Returns nil, false if raw is empty, invalid JSON, or not an object.
func ParseToolUseArguments(raw string) (map[string]any, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return m, true
}

// NormalizeToolUseArguments replaces non-object args with "{}" and returns an error string.
// Returns (normalized, errorMessage, ok). When ok is true, the input was a valid object.
func NormalizeToolUseArguments(raw, toolName string) (string, string, bool) {
	if _, ok := ParseToolUseArguments(raw); ok {
		return raw, "", true
	}
	return "{}", FormatToolUseArgumentsError(toolName, raw), false
}

// FormatToolUseArgumentsError builds a human-readable error for invalid tool arguments.
func FormatToolUseArgumentsError(toolName, raw string) string {
	const max = 80
	trimmed := raw
	if len(trimmed) > max {
		trimmed = trimmed[:max] + "..."
	}
	return fmt.Sprintf("tool %s: arguments must be a JSON object, got: %s", toolName, trimmed)
}


func ExtractJSON(jsonContent string, model any) error {
	candidates := extractJSONCandidates(jsonContent)
	if len(candidates) == 0 {
		return fmt.Errorf("no JSON found")
	}

	var lastErr error
	for _, candidate := range candidates {
		if err := decodeJSONCandidate(candidate, model); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no JSON found")
}

func extractJSONCandidates(content string) []string {
	var candidates []string
	for _, block := range extractMarkdownCodeBlocks(content) {
		trimmed := strings.TrimSpace(block)
		if strings.Contains(trimmed, "{") {
			candidates = append(candidates, trimmed)
		}
	}
	if balanced := extractBalancedJSONObject(content); balanced != "" {
		candidates = append(candidates, balanced)
	}
	return candidates
}

func extractMarkdownCodeBlocks(content string) []string {
	var blocks []string
	for offset := 0; offset < len(content); {
		start := strings.Index(content[offset:], "```")
		if start == -1 {
			break
		}
		start += offset

		headerEnd := strings.IndexByte(content[start+3:], '\n')
		if headerEnd == -1 {
			break
		}
		headerEnd += start + 3

		header := strings.TrimSpace(content[start+3 : headerEnd])
		if header != "" && !strings.EqualFold(header, "json") {
			offset = headerEnd + 1
			continue
		}

		bodyStart := headerEnd + 1
		end := strings.Index(content[bodyStart:], "```")
		if end == -1 {
			break
		}
		end += bodyStart
		blocks = append(blocks, content[bodyStart:end])
		offset = end + 3
	}
	return blocks
}

func extractBalancedJSONObject(content string) string {
	start := strings.Index(content, "{")
	if start == -1 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(content); i++ {
		ch := content[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1]
			}
		}
	}

	return ""
}

func decodeJSONCandidate(candidate string, model any) error {
	target := reflect.ValueOf(model)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return fmt.Errorf("model must be a non-nil pointer")
	}

	tmp := reflect.New(target.Elem().Type())
	decoder := json.NewDecoder(strings.NewReader(candidate))
	if err := decoder.Decode(tmp.Interface()); err != nil {
		return err
	}

	if err := ensureDecoderEOF(decoder); err != nil {
		return err
	}

	target.Elem().Set(tmp.Elem())
	return nil
}

func ensureDecoderEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	return fmt.Errorf("unexpected extra JSON content")
}
