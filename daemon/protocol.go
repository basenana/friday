package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/types"
)

const ProtocolVersion = 1

const (
	TypeSessionCreate      = "session.create"
	TypeSessionSubscribe   = "session.subscribe"
	TypeSessionUnsubscribe = "session.unsubscribe"
	TypeRun                = "run"
	TypeRunCancel          = "run.cancel"
	TypeInputCancel        = "input.cancel"
	TypeFormSubmit         = "form.submit"
	TypeFormCancel         = "form.cancel"

	TypeResult       = "result"
	TypeError        = "error"
	TypeEvent        = "event"
	TypeHistoryBegin = "history.begin"
	TypeHistoryEnd   = "history.end"
)

type ClientFrame struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"requestId"`
	ThreadID  string          `json:"threadId,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type ServerFrame struct {
	Version   int    `json:"version"`
	Type      string `json:"type"`
	RequestID string `json:"requestId,omitempty"`
	ThreadID  string `json:"threadId,omitempty"`
	Replay    bool   `json:"replay,omitempty"`
	Payload   any    `json:"payload,omitempty"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RunAgentInput is the AG-UI run input accepted by the daemon. Friday keeps
// the persisted session authoritative and consumes only new trailing user
// messages; the remaining fields are retained to validate the supported v1
// subset without changing AG-UI's wire shape.
type RunAgentInput struct {
	ThreadID       string            `json:"threadId"`
	RunID          string            `json:"runId"`
	ParentRunID    string            `json:"parentRunId,omitempty"`
	State          json.RawMessage   `json:"state,omitempty"`
	Messages       []AGUIMessage     `json:"messages"`
	Tools          []json.RawMessage `json:"tools,omitempty"`
	Context        []json.RawMessage `json:"context,omitempty"`
	ForwardedProps json.RawMessage   `json:"forwardedProps,omitempty"`
	Resume         []json.RawMessage `json:"resume,omitempty"`
}

type AGUIMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
}

type inputContent struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	MimeType string              `json:"mimeType,omitempty"`
	URL      string              `json:"url,omitempty"`
	Data     string              `json:"data,omitempty"`
	Filename string              `json:"filename,omitempty"`
	Source   *inputContentSource `json:"source,omitempty"`
}

type inputContentSource struct {
	Type     string `json:"type"`
	Value    string `json:"value"`
	MimeType string `json:"mimeType,omitempty"`
}

func (m AGUIMessage) userContent() (string, []types.ImageContent, error) {
	if m.Role != "user" {
		return "", nil, fmt.Errorf("message %q is not a user message", m.ID)
	}
	if len(m.Content) == 0 || bytes.Equal(bytes.TrimSpace(m.Content), []byte("null")) {
		return "", nil, errors.New("user message content is required")
	}
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return text, nil, nil
	}
	var parts []inputContent
	if err := json.Unmarshal(m.Content, &parts); err != nil {
		return "", nil, errors.New("user content must be a string or AG-UI content array")
	}
	var b strings.Builder
	var images []types.ImageContent
	for _, part := range parts {
		switch part.Type {
		case "text":
			b.WriteString(part.Text)
		case "image", "binary":
			image, err := part.image()
			if err != nil {
				return "", nil, err
			}
			images = append(images, image)
		default:
			return "", nil, fmt.Errorf("unsupported AG-UI content type %q", part.Type)
		}
	}
	return b.String(), images, nil
}

func (p inputContent) image() (types.ImageContent, error) {
	filename := p.Filename
	if p.Source != nil {
		switch p.Source.Type {
		case "url":
			if p.Source.Value == "" {
				return types.ImageContent{}, errors.New("image URL is required")
			}
			return types.ImageContent{Type: types.ImageTypeURL, URL: p.Source.Value, Filename: filename}, nil
		case "data":
			mime := p.Source.MimeType
			if mime == "" {
				mime = p.MimeType
			}
			if p.Source.Value == "" || mime == "" {
				return types.ImageContent{}, errors.New("image data and mimeType are required")
			}
			return types.ImageContent{Type: types.ImageTypeBase64, Data: p.Source.Value, MediaType: mime, Filename: filename}, nil
		default:
			return types.ImageContent{}, fmt.Errorf("unsupported image source %q", p.Source.Type)
		}
	}
	if p.URL != "" {
		return types.ImageContent{Type: types.ImageTypeURL, URL: p.URL, Filename: filename}, nil
	}
	if p.Data != "" && p.MimeType != "" {
		return types.ImageContent{Type: types.ImageTypeBase64, Data: p.Data, MediaType: p.MimeType, Filename: filename}, nil
	}
	return types.ImageContent{}, errors.New("image source is required")
}

func nonEmptyJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) &&
		!bytes.Equal(trimmed, []byte("{}")) && !bytes.Equal(trimmed, []byte("[]"))
}
