package session

import (
	"context"
)

// HookHandler is a function that can be registered to run at specific points in the session lifecycle.
type HookHandler func(ctx context.Context, sess *Session) error
