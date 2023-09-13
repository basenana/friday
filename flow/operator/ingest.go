package operator

import (
	"context"

	"github.com/basenana/go-flow/flow"

	flow2 "friday/flow"
)

type ingestOperator struct {
	command string
	spec    flow.Spec
}

func NewIngestOperator(task flow.Task, operatorSpec flow.Spec) (flow.Operator, error) {
	return &ingestOperator{spec: operatorSpec}, nil
}

func (i *ingestOperator) Do(ctx context.Context, param *flow.Parameter) error {
	knowledge := i.spec.Parameters["knowledge"]
	return flow2.FD.IngestFromElementFile(knowledge)
}
