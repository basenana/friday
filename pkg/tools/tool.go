package tools

import "context"

type Tool interface {
	Name() string
	Description() string
	APISchema() map[string]any
	Call(ctx context.Context, tool string, arguments map[string]interface{}) (string, error)
}
