package operator

import (
	"context"

	"github.com/basenana/go-flow/flow"

	fridayflow "friday/flow"
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
	return fridayflow.FD.IngestFromElementFile(knowledge)
}
