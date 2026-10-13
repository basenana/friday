package openairesponse

import "time"

// Model contains settings shared by all calls made through the Responses API.
type Model struct {
	Name               string
	Temperature        *float64
	MaxTokens          int64
	ReasoningEffort    string
	StrictMode         bool
	QPM                int64
	Proxy              string
	ContextWindow      int64
	InsecureSkipVerify bool
	// Timeout caps one HTTP request including streaming body reads.
	// Zero uses defaultHTTPTimeout (15m).
	Timeout time.Duration
}
