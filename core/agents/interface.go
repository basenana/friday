package agents

import (
	"context"

	"github.com/basenana/friday/core/agents/agtapi"
)

type Agent interface {
	Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response
}
