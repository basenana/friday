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
			res := ChatState{}
			f := loFriday.WithContext(context.TODO()).SearchIn(&models.DocQuery{}).Question("I am a question").Complete(&res)
			Expect(f.Error).Should(BeNil())
			Expect(res.Answer).Should(Equal("I am an answer"))
		})
		It("searchDocs should be succeed", func() {
			f := loFriday.WithContext(context.TODO())
			f.searchDocs("I am a question")
			Expect(f.Error).Should(BeNil())
			Expect(f.statement.info).Should(Equal("There are logs of questions"))
		})
	})

	Context("chat", func() {
		It("chat for first time", func() {
			history := []map[string]string{{
				"role":    "user",
				"content": "Who are you?",
			}}
			res := ChatState{}
			f := loFriday.WithContext(context.TODO()).SearchIn(&models.DocQuery{ParentId: 1}).History(history).Chat(&res)
			Expect(f.Error).Should(BeNil())
			Expect(len(res.Dialogues)).Should(Equal(3))
			Expect(res.Dialogues[0]["role"]).Should(Equal("system"))
			Expect(res.Dialogues[0]["content"]).Should(Equal("基于以下已知信息，简洁和专业的来回答用户的问题。答案请使用中文。 \n\n已知内容: There are logs of questions"))
			Expect(res.Dialogues[1]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[2]["role"]).Should(Equal("assistant"))
		})
		It("chat for second time", func() {
			history := []map[string]string{
				{
					"role":    "system",
					"content": "基于以下已知信息，简洁和专业的来回答用户的问题。答案请使用中文。",
				},
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
			res := ChatState{}
			f := loFriday.WithContext(context.TODO()).SearchIn(&models.DocQuery{ParentId: 1}).History(history).Chat(&res)
			Expect(f.Error).Should(BeNil())
			Expect(len(res.Dialogues)).Should(Equal(5))
			Expect(res.Dialogues[0]["role"]).Should(Equal("system"))
			Expect(res.Dialogues[0]["content"]).Should(Equal("基于以下已知信息，简洁和专业的来回答用户的问题。答案请使用中文。 \n\n已知内容: There are logs of questions"))
			Expect(res.Dialogues[1]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[2]["role"]).Should(Equal("assistant"))
			Expect(res.Dialogues[3]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[4]["role"]).Should(Equal("assistant"))
		})
		It("chat for three times", func() {
			history := []map[string]string{
				{
					"role":    "system",
					"content": "基于以下已知信息，简洁和专业的来回答用户的问题。答案请使用中文。",
				},
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
			res := ChatState{}
			f := loFriday.WithContext(context.TODO()).SearchIn(&models.DocQuery{ParentId: 1}).History(history).Chat(&res)
			Expect(f.Error).Should(BeNil())
			Expect(len(res.Dialogues)).Should(Equal(8))
			Expect(res.Dialogues[0]["role"]).Should(Equal("system"))
			Expect(res.Dialogues[0]["content"]).Should(Equal("基于以下已知信息，简洁和专业的来回答用户的问题。答案请使用中文。 \n\n已知内容: There are logs of questions"))
			Expect(res.Dialogues[1]["role"]).Should(Equal("system"))
			Expect(res.Dialogues[1]["content"]).Should(Equal("这是历史聊天总结作为前情提要：I am an answer"))
			Expect(res.Dialogues[2]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[2]["content"]).Should(Equal("one"))
			Expect(res.Dialogues[3]["role"]).Should(Equal("assistant"))
			Expect(res.Dialogues[4]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[4]["content"]).Should(Equal("two"))
			Expect(res.Dialogues[5]["role"]).Should(Equal("assistant"))
			Expect(res.Dialogues[6]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[6]["content"]).Should(Equal("three"))
			Expect(res.Dialogues[7]["role"]).Should(Equal("assistant"))
		})
		It("chat for four times", func() {
			history := []map[string]string{
				{
					"role":    "system",
					"content": "基于以下已知信息，简洁和专业的来回答用户的问题。答案请使用中文。",
				},
				{
					"role":    "system",
					"content": "这是历史聊天总结作为前情提要：I am an answer",
				},
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
			res := ChatState{}
			f := loFriday.WithContext(context.TODO()).SearchIn(&models.DocQuery{ParentId: 1}).History(history).Chat(&res)
			Expect(f.Error).Should(BeNil())
			Expect(len(res.Dialogues)).Should(Equal(8))
			Expect(res.Dialogues[0]["role"]).Should(Equal("system"))
			Expect(res.Dialogues[0]["content"]).Should(Equal("基于以下已知信息，简洁和专业的来回答用户的问题。答案请使用中文。 \n\n已知内容: There are logs of questions"))
			Expect(res.Dialogues[1]["role"]).Should(Equal("system"))
			Expect(res.Dialogues[1]["content"]).Should(Equal("这是历史聊天总结作为前情提要：I am an answer"))
			Expect(res.Dialogues[2]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[2]["content"]).Should(Equal("two"))
			Expect(res.Dialogues[3]["role"]).Should(Equal("assistant"))
			Expect(res.Dialogues[4]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[4]["content"]).Should(Equal("three"))
			Expect(res.Dialogues[5]["role"]).Should(Equal("assistant"))
			Expect(res.Dialogues[6]["role"]).Should(Equal("user"))
			Expect(res.Dialogues[6]["content"]).Should(Equal("four"))
			Expect(res.Dialogues[7]["role"]).Should(Equal("assistant"))
		})
	})
})

type FakeQuestionStore struct{}

var _ vectorstore.VectorStore = &FakeQuestionStore{}

func (f FakeQuestionStore) Store(ctx context.Context, element *models.Element, extra map[string]any) error {
	return nil
}

func (f FakeQuestionStore) Search(ctx context.Context, query models.DocQuery, vectors []float32, k int) ([]*models.Doc, error) {
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
	return []string{"I am an answer"}, nil, nil
}

func (f FakeQuestionLLM) Chat(ctx context.Context, history []map[string]string) (answers map[string]string, tokens map[string]int, err error) {
	return map[string]string{"role": "assistant", "content": "I am an answer"}, nil, nil
}
