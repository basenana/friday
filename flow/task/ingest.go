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
	"path/filepath"

	"github.com/basenana/go-flow/exec"
	goflow "github.com/basenana/go-flow/flow"
)

func NewIngestTaskInShell(binDir, knowledge string) (goflow.Task, error) {
	return goflow.Task{
		Name: "ingest",
		OperatorSpec: goflow.Spec{
			Type: exec.ShellOperator,
			Script: &goflow.Script{
				Command: []string{filepath.Join(binDir, "friday"), "ingest", knowledge, "--config", filepath.Join(binDir, "friday.conf")},
			},
		},
	}, nil
}

func NewIngestTask(name, knowledge string) (goflow.Task, error) {
	return goflow.Task{
		Name: "ingest",
		OperatorSpec: goflow.Spec{
			Type: "IngestOperator",
			Parameters: map[string]string{
				"source":    name,
				"knowledge": knowledge,
			},
		},
	}, nil
}
