package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ToolSet interface {
	List() []*Tool
}

type ToolHandlerFunc func(ctx context.Context, request *Request) (*Result, error)

// SessionRecords is a session-bound auxiliary record store exposed to tools.
// Implementations decide whether records are persisted; temporary sessions may
// keep them in memory only.
type SessionRecords interface {
	ReadRecord(ctx context.Context, namespace string) ([]byte, error)
	UpdateRecord(ctx context.Context, namespace string, update func([]byte) ([]byte, error)) error
}

type Tool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Annotations map[string]string `json:"annotations,omitempty"`
	InputSchema ToolInputSchema   `json:"inputSchema"`
	// RawInputSchema preserves an externally supplied JSON Schema (for example,
	// from MCP) without narrowing it to the subset built by this package.
	RawInputSchema map[string]interface{}   `json:"-"`
	Examples       []map[string]interface{} `json:"-"`
	Handler        ToolHandlerFunc          `json:"-"`
}

func (t *Tool) JsonSchema() map[string]interface{} {
	if len(t.RawInputSchema) > 0 {
		return cloneSchemaMap(t.RawInputSchema)
	}
	required := t.InputSchema.Required
	if required == nil {
		required = []string{}
	}
	return map[string]interface{}{
		"type":                 "object",
		"properties":           t.InputSchema.Properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func cloneSchemaMap(schema map[string]interface{}) map[string]interface{} {
	raw, err := json.Marshal(schema)
	if err != nil {
		return schema
	}
	var cloned map[string]interface{}
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return schema
	}
	return cloned
}

func (t *Tool) GetName() string { return t.Name }
func (t *Tool) GetDescription() string {
	description := strings.TrimSpace(t.Description)
	for i, example := range t.Examples {
		raw, err := json.Marshal(example)
		if err != nil {
			continue
		}
		label := "Example"
		if len(t.Examples) > 1 {
			label = fmt.Sprintf("Example %d", i+1)
		}
		description += fmt.Sprintf("\n\n%s:\n%s", label, raw)
	}
	return description
}
func (t *Tool) GetParameters() map[string]any { return t.JsonSchema() }

func NewTool(name string, options ...ToolOption) *Tool {
	t := &Tool{
		Name:        name,
		InputSchema: ToolInputSchema{Properties: make(map[string]interface{})},
	}

	for _, opt := range options {
		opt(t)
	}
	return t
}

type ToolInputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

type Request struct {
	Arguments      map[string]interface{} `json:"arguments"`
	SessionID      string                 `json:"sessionId"`
	SessionRecords SessionRecords         `json:"-"`
}

type Result struct {
	Content     []Content `json:"content"`
	FYI         string    `json:"fyi,omitempty"`
	IsError     bool      `json:"is_error,omitempty"`
	Retryable   bool      `json:"retryable,omitempty"`
	RetryReason string    `json:"retry_reason,omitempty"`
	ExitCode    *int      `json:"exit_code,omitempty"`
	Cancelled   bool      `json:"cancelled,omitempty"`
}

// NewToolResultRetryableError marks a failure as safe for an execution-plan retry.
// Callers must only use this when the operation is idempotent or has a stable dedupe key.
func NewToolResultRetryableError(text, reason string) *Result {
	result := NewToolResultError(text)
	result.Retryable = true
	result.RetryReason = reason
	return result
}

// NewToolResultText creates a new CallToolResult with a text content
func NewToolResultText(text string) *Result {
	return &Result{
		Content: []Content{
			TextContent{
				Type: "text",
				Text: text,
			},
		},
	}
}

// NewToolResultError creates a new CallToolResult with an error message.
// Any errors that originate from the tool SHOULD be reported inside the result object.
func NewToolResultError(text string) *Result {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "Tool failed without an error message."
	}
	if !strings.Contains(strings.ToLower(text), "suggestion:") {
		text += "\nSuggestion: use the error above to correct the arguments or prerequisites before retrying; do not repeat the unchanged call."
	}
	return &Result{
		Content: []Content{
			TextContent{
				Type: "text",
				Text: text,
			},
		},
		IsError: true,
	}
}

// NewToolResultActionableError creates a model-visible failure with a concrete
// next step. Use this for failures the caller can correct and retry.
func NewToolResultActionableError(cause, suggestion string) *Result {
	cause = strings.TrimSpace(cause)
	suggestion = strings.TrimSpace(suggestion)
	if cause == "" {
		cause = "Tool failed without an error message."
	}
	if suggestion != "" {
		cause += "\nSuggestion: " + suggestion
	}
	return NewToolResultError(cause)
}

type Content interface {
	isContent()
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (t TextContent) isContent() {}

var _ Content = TextContent{}

type PropertyOption func(map[string]interface{})

type ToolOption func(*Tool)

// WithDescription adds a description to the Tool.
// The description should provide a clear, human-readable explanation of what the tool does.
func WithDescription(description string) ToolOption {
	return func(t *Tool) {
		t.Description = description
	}
}

func WithToolAnnotations(annotations map[string]string) ToolOption {
	return func(t *Tool) {
		t.Annotations = annotations
	}
}

func WithToolHandler(handler ToolHandlerFunc) ToolOption {
	return func(t *Tool) {
		t.Handler = handler
	}
}

// WithExample adds one complete, valid invocation example. Providers append
// examples to the model-visible description in a consistent format.
func WithExample(example map[string]interface{}) ToolOption {
	return func(t *Tool) {
		t.Examples = append(t.Examples, example)
	}
}

//
// Common Property Options
//

// Description adds a description to a property in the JSON Schema.
// The description should explain the purpose and expected values of the property.
func Description(desc string) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["description"] = desc
	}
}

// Required marks a property as required in the tool's input schema.
// Required properties must be provided when using the tool.
func Required() PropertyOption {
	return func(schema map[string]interface{}) {
		schema["required"] = true
	}
}

// Title adds a display-friendly title to a property in the JSON Schema.
// This title can be used by UI components to show a more readable property name.
func Title(title string) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["title"] = title
	}
}

//
// String Property Options
//

// DefaultString sets the default value for a string property.
// This value will be used if the property is not explicitly provided.
func DefaultString(value string) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["default"] = value
	}
}

// Enum specifies a list of allowed values for a string property.
// The property value must be one of the specified enum values.
func Enum(values ...string) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["enum"] = values
	}
}

// MaxLength sets the maximum length for a string property.
// The string value must not exceed this length.
func MaxLength(max int) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["maxLength"] = max
	}
}

// MinLength sets the minimum length for a string property.
// The string value must be at least this length.
func MinLength(min int) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["minLength"] = min
	}
}

// Pattern sets a regex pattern that a string property must match.
// The string value must conform to the specified regular expression.
func Pattern(pattern string) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["pattern"] = pattern
	}
}

//
// Number Property Options
//

// DefaultNumber sets the default value for a number property.
// This value will be used if the property is not explicitly provided.
func DefaultNumber(value float64) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["default"] = value
	}
}

// Max sets the maximum value for a number property.
// The number value must not exceed this maximum.
func Max(max float64) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["maximum"] = max
	}
}

// Min sets the minimum value for a number property.
// The number value must not be less than this minimum.
func Min(min float64) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["minimum"] = min
	}
}

// MultipleOf specifies that a number must be a multiple of the given value.
// The number value must be divisible by this value.
func MultipleOf(value float64) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["multipleOf"] = value
	}
}

//
// Boolean Property Options
//

// DefaultBool sets the default value for a boolean property.
// This value will be used if the property is not explicitly provided.
func DefaultBool(value bool) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["default"] = value
	}
}

//
// Array Property Options
//

// DefaultArray sets the default value for an array property.
// This value will be used if the property is not explicitly provided.
func DefaultArray[T any](value []T) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["default"] = value
	}
}

//
// Property Type Helpers
//

func WithBoolean(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "boolean", opts)
}

func WithNumber(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "number", opts)
}

func WithInteger(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "integer", opts)
}

func WithString(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "string", opts)
}

func WithObject(name string, opts ...PropertyOption) ToolOption {
	return func(t *Tool) {
		addProperty(t, name, map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}, opts)
	}
}

func WithArray(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "array", opts)
}

func withProperty(name, propertyType string, opts []PropertyOption) ToolOption {
	return func(t *Tool) {
		addProperty(t, name, map[string]interface{}{"type": propertyType}, opts)
	}
}

func addProperty(t *Tool, name string, schema map[string]interface{}, opts []PropertyOption) {
	for _, opt := range opts {
		opt(schema)
	}
	if required, _ := schema["required"].(bool); required {
		delete(schema, "required")
		t.InputSchema.Required = append(t.InputSchema.Required, name)
	}
	t.InputSchema.Properties[name] = schema
}

// Properties defines the properties for an object schema
func Properties(props map[string]interface{}) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["properties"] = props
	}
}

// AdditionalProperties specifies whether additional properties are allowed in the object
// or defines a schema for additional properties
func AdditionalProperties(schema interface{}) PropertyOption {
	return func(schemaMap map[string]interface{}) {
		schemaMap["additionalProperties"] = schema
	}
}

// MinProperties sets the minimum number of properties for an object
func MinProperties(min int) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["minProperties"] = min
	}
}

// MaxProperties sets the maximum number of properties for an object
func MaxProperties(max int) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["maxProperties"] = max
	}
}

// PropertyNames defines a schema for property names in an object
func PropertyNames(schema map[string]interface{}) PropertyOption {
	return func(schemaMap map[string]interface{}) {
		schemaMap["propertyNames"] = schema
	}
}

// Items defines the schema for array items
func Items(schema interface{}) PropertyOption {
	return func(schemaMap map[string]interface{}) {
		schemaMap["items"] = schema
	}
}

// MinItems sets the minimum number of items for an array
func MinItems(min int) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["minItems"] = min
	}
}

// MaxItems sets the maximum number of items for an array
func MaxItems(max int) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["maxItems"] = max
	}
}

// UniqueItems specifies whether array items must be unique
func UniqueItems(unique bool) PropertyOption {
	return func(schema map[string]interface{}) {
		schema["uniqueItems"] = unique
	}
}
