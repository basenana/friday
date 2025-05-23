/*
 Copyright 2023 Friday Author.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package friday

import (
	"context"

	"github.com/basenana/friday/pkg/embedding"
	"github.com/basenana/friday/pkg/friday/summary"
	"github.com/basenana/friday/pkg/llm"
	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/models/vector"
	"github.com/basenana/friday/pkg/spliter"
	"github.com/basenana/friday/pkg/store"
	"github.com/basenana/friday/pkg/utils/logger"
)

const (
	DefaultTopK               = 6
	questionPromptKey         = "question"
	keywordsPromptKey         = "keywords"
	wechatPromptKey           = "wechat"
	wechatConclusionPromptKey = "wechat-conclusion"
)

var (
	Fri *Friday
)

type Friday struct {
	Log       logger.Logger
	Error     error
	statement Statement

	LimitToken int

	LLM     llm.LLM
	Prompts map[string]string

	Embedding embedding.Embedding

	Vector     store.VectorStore
	VectorTopK *int

	Spliter spliter.Spliter
}

type Statement struct {
	context context.Context

	// for chat
	Summary        string                 // Summary of doc
	HistorySummary string                 // Summary of chat history
	Info           string                 // Info of embedding
	history        []map[string]string    // real chat history
	question       string                 // question for chat
	query          *vector.VectorDocQuery // search in doc or dir

	// for ingest or Summary
	file        *vector.File // a whole file providing models.File
	elementFile *string      // a whole file given an element-style origin file
	originFile  *string      // a whole file given an origin file
	elements    []vector.Element

	// for keywords
	content string

	// for Summary
	summaryType summary.SummaryType
}

type ChatState struct {
	Response chan map[string]string // dialogue result for chat
	Answer   string                 // answer result for question
	Tokens   map[string]int
}

type KeywordsState struct {
	Keywords []string
	Tokens   map[string]int
}

type SummaryState struct {
	Summary map[string]string
	Tokens  map[string]int
}

type IngestState struct {
	Tokens map[string]int
}

func (f *Friday) WithContext(ctx context.Context) *Friday {
	t := &Friday{
		Log:        f.Log,
		statement:  Statement{context: ctx},
		LimitToken: f.LimitToken,
		LLM:        f.LLM,
		Prompts:    f.Prompts,
		Embedding:  f.Embedding,
		Vector:     f.Vector,
		VectorTopK: f.VectorTopK,
		Spliter:    f.Spliter,
	}
	return t
}

func (f *Friday) Namespace(namespace *models.Namespace) *Friday {
	f.statement.context = models.WithNamespace(f.statement.context, namespace)
	return f
}
