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
