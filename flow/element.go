package flow

import (
	"github.com/basenana/go-flow/exec"
	goflow "github.com/basenana/go-flow/flow"
)

func NewElementFlow(id string, binDir, input, output string) goflow.Flow {
	return goflow.Flow{
		ID:            id,
		Describe:      "Split element file by row.",
		Executor:      "local",
		ControlPolicy: goflow.ControlPolicy{FailedPolicy: goflow.PolicyFastFailed},
		Tasks:         []goflow.Task{NewElementTask(binDir, input, output)},
	}
}

func NewElementTask(binDir, input, output string) goflow.Task {
	elementPath := binDir + "/base/element.py"
	return goflow.Task{
		Name: "split element by row.",
		OperatorSpec: goflow.Spec{
			Type: exec.PythonOperator,
			Script: &goflow.Script{
				Command: []string{"python3", elementPath, input, output},
				Env: map[string]string{
					"NLTK_DATA":  binDir + "/base/nltk_data",
					"PYTHONPATH": "$PYTHONPATH:" + binDir + "/base",
				},
			},
		},
	}
}
