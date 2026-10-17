package agents

import (
	"context"

	"git.hypo.xyz/agents/friday/core/api"
)

type Agent interface {
	Chat(ctx context.Context, req *api.Request) *api.Response
}
