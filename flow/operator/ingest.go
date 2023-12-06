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

package operator

import (
	"context"

	"github.com/basenana/go-flow/flow"

	"github.com/basenana/friday/pkg/friday"
	"github.com/basenana/friday/pkg/models"
)

type ingestOperator struct {
	command string
	spec    flow.Spec
}

func NewIngestOperator(task flow.Task, operatorSpec flow.Spec) (flow.Operator, error) {
	return &ingestOperator{spec: operatorSpec}, nil
}

func (i *ingestOperator) Do(ctx context.Context, param *flow.Parameter) error {
	source := i.spec.Parameters["source"]
	knowledge := i.spec.Parameters["knowledge"]
	doc := models.File{
		Name:    source,
		Content: knowledge,
	}
	return friday.Fri.IngestFromFile(context.TODO(), doc)
}
