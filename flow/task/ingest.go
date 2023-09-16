package task

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/basenana/go-flow/exec"
	goflow "github.com/basenana/go-flow/flow"
)

func NewIngestTaskInShell(binDir, knowledge string) (goflow.Task, error) {
	openaiKey := os.Getenv("OPENAI_KEY")
	if openaiKey == "" {
		return goflow.Task{}, fmt.Errorf("OPENAI_KEY is not set")
	}
	envs := map[string]string{
		"OPENAI_KEY": openaiKey,
	}
	return goflow.Task{
		Name: "ingest",
		OperatorSpec: goflow.Spec{
			Type: exec.ShellOperator,
			Script: &goflow.Script{
				Command: []string{filepath.Join(binDir, "friday"), "ingest", knowledge, "--config", filepath.Join(binDir, "friday.conf")},
				Env:     envs,
			},
		},
	}, nil
}

func NewIngestTask(knowledge string) (goflow.Task, error) {
	return goflow.Task{
		Name: "ingest",
		OperatorSpec: goflow.Spec{
			Type: "IngestOperator",
			Parameters: map[string]string{
				"knowledge": knowledge,
			},
		},
	}, nil
}