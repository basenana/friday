/*
 * Copyright 2023 friday
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package task

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/basenana/go-flow/exec"
	goflow "github.com/basenana/go-flow/flow"

	"github.com/basenana/friday/config"
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
	return goflow.Task{
		Name: "question",
		OperatorSpec: goflow.Spec{
			Type: exec.ShellOperator,
			Script: &goflow.Script{
				Command: []string{"sh", "-c", fmt.Sprintf("%s question %s --config %s > %s/output.txt", filepath.Join(binDir, "friday"), question, filepath.Join(binDir, "friday.conf"), binDir)},
			},
		},
	}, nil
}
