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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/basenana/friday/pkg/embedding"
	"github.com/basenana/friday/pkg/llm"
	"github.com/basenana/friday/pkg/llm/prompts"
	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/spliter"
	"github.com/basenana/friday/pkg/utils/logger"
	"github.com/basenana/friday/pkg/vectorstore"
)

var _ = Describe("TestQuestion", func() {
	var (
		loFriday = &Friday{}
	)

	BeforeEach(func() {
		topk := 6
		loFriday.Vector = FakeStore{}
		loFriday.Log = logger.NewLogger("test-question")
		loFriday.Spliter = spliter.NewTextSpliter(loFriday.Log, spliter.DefaultChunkSize, spliter.DefaultChunkOverlap, "\n")
		loFriday.Embedding = FakeQuestionEmbedding{}
		loFriday.LLM = FakeQuestionLLM{}
		loFriday.Vector = FakeQuestionStore{}
		loFriday.VectorTopK = &topk
	})

	Context("question", func() {
		It("question should be succeed", func() {
			ans, _, err := loFriday.Question(context.TODO(), 0, "I am a question")
			Expect(err).Should(BeNil())
			Expect(ans).Should(Equal("I am an answer"))
		})
		It("searchDocs should be succeed", func() {
			ans, err := loFriday.searchDocs(context.TODO(), 0, "I am a question")
			Expect(err).Should(BeNil())
			Expect(ans).Should(Equal("There are logs of questions"))
		})
	})
})

type FakeQuestionStore struct{}

var _ vectorstore.VectorStore = &FakeQuestionStore{}

func (f FakeQuestionStore) Store(ctx context.Context, element *models.Element, extra map[string]any) error {
	return nil
}

func (f FakeQuestionStore) Search(ctx context.Context, parentId int64, vectors []float32, k int) ([]*models.Doc, error) {
	return []*models.Doc{{
		Id:      "abc",
		Content: "There are logs of questions",
	}}, nil
}

func (f FakeQuestionStore) Get(ctx context.Context, oid int64, name string, group int) (*models.Element, error) {
	return &models.Element{}, nil
}

type FakeQuestionEmbedding struct{}

var _ embedding.Embedding = FakeQuestionEmbedding{}

func (f FakeQuestionEmbedding) VectorQuery(ctx context.Context, doc string) ([]float32, map[string]interface{}, error) {
	return []float32{}, map[string]interface{}{}, nil
}

func (f FakeQuestionEmbedding) VectorDocs(ctx context.Context, docs []string) ([][]float32, []map[string]interface{}, error) {
	return [][]float32{}, []map[string]interface{}{}, nil
}

type FakeQuestionLLM struct{}

var _ llm.LLM = &FakeQuestionLLM{}

func (f FakeQuestionLLM) Completion(ctx context.Context, prompt prompts.PromptTemplate, parameters map[string]string) ([]string, map[string]int, error) {
	return []string{}, nil, nil
}

func (f FakeQuestionLLM) Chat(ctx context.Context, prompt prompts.PromptTemplate, parameters map[string]string) ([]string, map[string]int, error) {
	return []string{"I am an answer"}, nil, nil
}
