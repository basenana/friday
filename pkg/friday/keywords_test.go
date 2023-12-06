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

	"github.com/basenana/friday/pkg/llm"
	"github.com/basenana/friday/pkg/llm/prompts"
	"github.com/basenana/friday/pkg/utils/logger"
)

var _ = Describe("TestKeywords", func() {
	var (
		loFriday = &Friday{}
	)

	BeforeEach(func() {
		loFriday.LLM = FakeKeyWordsLLM{}
		loFriday.Log = logger.NewLogger("test-keywords")
	})

	Context("keywords", func() {
		It("keywords should be succeed", func() {
			keywords, _, err := loFriday.Keywords(context.TODO(), "test")
			Expect(err).Should(BeNil())
			Expect(keywords).Should(Equal([]string{"a", "b", "c"}))
		})
	})
})

type FakeKeyWordsLLM struct{}

var _ llm.LLM = &FakeKeyWordsLLM{}

func (f FakeKeyWordsLLM) Completion(ctx context.Context, prompt prompts.PromptTemplate, parameters map[string]string) ([]string, map[string]int, error) {
	return []string{}, nil, nil
}

func (f FakeKeyWordsLLM) Chat(ctx context.Context, prompt prompts.PromptTemplate, parameters map[string]string) ([]string, map[string]int, error) {
	return []string{"a, b, c"}, nil, nil
}
