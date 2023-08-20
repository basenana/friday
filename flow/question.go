package flow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/basenana/go-flow/exec"
	goflow "github.com/basenana/go-flow/flow"

	"friday/config"
)

func NewConfigTask(binDir string, config config.Config) (goflow.Task, error) {
	configByte, err := json.Marshal(config)
	if err != nil {
		return goflow.Task{}, err
	}

	return goflow.Task{
		Name: "set config",
		OperatorSpec: goflow.Spec{
			Type:   exec.ShellOperator,
			Script: &goflow.Script{Command: []string{"sh", "-c", fmt.Sprintf("echo '%s' > %s", string(configByte), filepath.Join(binDir, "friday.conf"))}},
		},
	}, nil
}

func NewQuestionTask(binDir, question string) (goflow.Task, error) {
	openaiKey := os.Getenv("OPENAI_KEY")
	if openaiKey == "" {
		return goflow.Task{}, fmt.Errorf("OPENAI_KEY is not set")
	}
	envs := map[string]string{
		"OPENAI_KEY": openaiKey,
	}
	return goflow.Task{
		Name: "question",
		OperatorSpec: goflow.Spec{
			Type: exec.ShellOperator,
			Script: &goflow.Script{
				Command: []string{"sh", "-c", fmt.Sprintf("%s question %s --config %s > %s/output.txt", filepath.Join(binDir, "friday"), question, filepath.Join(binDir, "friday.conf"), binDir)},
				Env:     envs,
			},
		},
	}, nil
}
