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
	"github.com/basenana/friday/pkg/models/vector"
	"github.com/basenana/friday/pkg/spliter"
	"github.com/basenana/friday/pkg/store/vectorstore"
	"github.com/basenana/friday/pkg/utils/logger"
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
			res := ChatState{}
			f := loFriday.WithContext(context.TODO()).SearchIn(&vector.VectorDocQuery{}).Question("I am a question").Complete(&res)
			Expect(f.Error).Should(BeNil())
			Expect(res.Answer).Should(Equal("I am an answer"))
		})
		It("searchDocs should be succeed", func() {
			f := loFriday.WithContext(context.TODO())
			f.searchDocs("I am a question")
			Expect(f.Error).Should(BeNil())
			Expect(f.statement.Info).Should(Equal("There are logs of questions"))
		})
	})

	Context("chat", func() {
		It("chat for first time", func() {
			history := []map[string]string{{
				"role":    "user",
				"content": "Who are you?",
			}}
			var (
				res = ChatState{
					Response: make(chan map[string]string),
				}
				f *Friday
			)
			go func() {
				f = loFriday.WithContext(context.TODO()).SearchIn(&vector.VectorDocQuery{ParentId: 1}).History(history).Chat(&res)
			}()
			resp := <-res.Response
			Expect(f.Error).Should(BeNil())
			Expect(len(resp)).Should(Equal(2))
			Expect(resp["role"]).Should(Equal("assistant"))
			Expect(len(f.statement.history)).Should(Equal(3))
		})
		It("chat for second time", func() {
			history := []map[string]string{
				{
					"role":    "user",
					"content": "Who are you?",
				},
				{
					"role":    "assistant",
					"content": "I am an answer",
				},
				{
					"role":    "user",
					"content": "abc",
				},
			}
			var (
				res = ChatState{
					Response: make(chan map[string]string),
				}
				f *Friday
			)
			go func() {
				f = loFriday.WithContext(context.TODO()).SearchIn(&vector.VectorDocQuery{ParentId: 1}).History(history).Chat(&res)
			}()
			resp := <-res.Response
			Expect(f.Error).Should(BeNil())
			Expect(len(resp)).Should(Equal(2))
			Expect(resp["role"]).Should(Equal("assistant"))
			Expect(len(f.statement.history)).Should(Equal(5))
		})
		It("chat for three times", func() {
			history := []map[string]string{
				{
					"role":    "user",
					"content": "one",
				},
				{
					"role":    "assistant",
					"content": "I am an answer",
				},
				{
					"role":    "user",
					"content": "two",
				},
				{
					"role":    "assistant",
					"content": "I am an answer",
				},
				{
					"role":    "user",
					"content": "three",
				},
			}
			var (
				res = ChatState{
					Response: make(chan map[string]string),
				}
				f *Friday
			)
			go func() {
				f = loFriday.WithContext(context.TODO()).SearchIn(&vector.VectorDocQuery{ParentId: 1}).History(history).Chat(&res)
			}()
			resp := <-res.Response

			Expect(f.Error).Should(BeNil())
			Expect(len(resp)).Should(Equal(2))
			Expect(resp["role"]).Should(Equal("assistant"))
			Expect(len(f.statement.history)).Should(Equal(7))
		})
		It("chat for four times", func() {
			history := []map[string]string{
				{
					"role":    "user",
					"content": "one",
				},
				{
					"role":    "assistant",
					"content": "I am an answer",
				},
				{
					"role":    "user",
					"content": "two",
				},
				{
					"role":    "assistant",
					"content": "I am an answer",
				},
				{
					"role":    "user",
					"content": "three",
				},
				{
					"role":    "assistant",
					"content": "I am an answer",
				},
				{
					"role":    "user",
					"content": "four",
				},
			}
			var (
				res = ChatState{
					Response: make(chan map[string]string),
				}
				f *Friday
			)
			go func() {
				f = loFriday.WithContext(context.TODO()).SearchIn(&vector.VectorDocQuery{ParentId: 1}).History(history).Chat(&res)
			}()
			resp := <-res.Response
			Expect(f.Error).Should(BeNil())
			Expect(len(resp)).Should(Equal(2))
			Expect(resp["role"]).Should(Equal("assistant"))
			Expect(len(f.statement.history)).Should(Equal(7))
		})
	})
})

type FakeQuestionStore struct{}

var _ vectorstore.VectorStore = &FakeQuestionStore{}

func (f FakeQuestionStore) Store(ctx context.Context, element *vector.Element, extra map[string]any) error {
	return nil
}

func (f FakeQuestionStore) Search(ctx context.Context, query vector.VectorDocQuery, vectors []float32, k int) ([]*vector.Doc, error) {
	return []*vector.Doc{{
		Id:      "abc",
		Content: "There are logs of questions",
	}}, nil
}

func (f FakeQuestionStore) Get(ctx context.Context, oid int64, name string, group int) (*vector.Element, error) {
	return &vector.Element{}, nil
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

func (f FakeQuestionLLM) GetUserModel() string {
	return "user"
}

func (f FakeQuestionLLM) GetSystemModel() string {
	return "system"
}

func (f FakeQuestionLLM) GetAssistantModel() string {
	return "assistant"
}

func (f FakeQuestionLLM) Completion(ctx context.Context, prompt prompts.PromptTemplate, parameters map[string]string) ([]string, map[string]int, error) {
	return []string{"I am an answer"}, nil, nil
}

func (f FakeQuestionLLM) Chat(ctx context.Context, stream bool, history []map[string]string, answers chan<- map[string]string) (tokens map[string]int, err error) {
	answers <- map[string]string{"role": "assistant", "content": "I am an answer"}
	return nil, nil
}
