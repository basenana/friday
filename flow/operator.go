package flow

import (
	"context"

	"github.com/basenana/go-flow/flow"
)

type goOperator struct {
	command string
	spec    flow.Spec
}

func (g *goOperator) Do(ctx context.Context, param *flow.Parameter) error {
	return nil
}
