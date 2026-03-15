package state

import "context"

type StateScope string

const (
	ScopeApp  StateScope = "app"
	ScopeUser StateScope = "user"
)

type State interface {
	Get(ctx context.Context, scope StateScope, key string) (string, error)
	Set(ctx context.Context, scope StateScope, key string, value string) error
	Delete(ctx context.Context, scope StateScope, key string) error
	List(ctx context.Context, scope StateScope) ([]string, error)
	WithUser(userID string) State
}
