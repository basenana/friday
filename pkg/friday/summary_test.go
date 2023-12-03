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

	"github.com/basenana/friday/pkg/friday/summary"
	"github.com/basenana/friday/pkg/llm"
	"github.com/basenana/friday/pkg/llm/prompts"
	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/spliter"
	"github.com/basenana/friday/pkg/utils/logger"
)

var _ = Describe("TestStuffSummary", func() {
	var (
		loFriday    = &Friday{}
		summaryType = summary.Stuff
		elements    = []models.Element{}
		file        = models.File{}
	)

	BeforeEach(func() {
		loFriday.Log = logger.NewLogger("test-stuffsummary")
		loFriday.LLM = FakeSummaryLLM{}
		loFriday.Spliter = spliter.NewTextSpliter(loFriday.Log, spliter.DefaultChunkSize, spliter.DefaultChunkOverlap, "\n")
		elements = []models.Element{{
			Content: "test-content",
			Metadata: models.Metadata{
				Source:    "test-source",
				Title:     "test-title",
				ParentDir: "/",
			},
		}}
		file = models.File{
			Name:    "test-file",
			Source:  "test-file-source",
			Content: "test-file-content",
		}
	})

	Context("summary", func() {
		It("summary should be succeed", func() {
			summary, err := loFriday.Summary(context.TODO(), elements, summaryType)
			Expect(err).Should(BeNil())
			Expect(summary).Should(Equal(map[string]string{
				"test-source": "a b c",
			}))
		})
		It("SummaryFromFile should be succeed", func() {
			summary, err := loFriday.SummaryFromFile(context.TODO(), file, summaryType)
			Expect(err).Should(BeNil())
			Expect(summary).Should(Equal(map[string]string{
				"test-file-source": "a b c",
			}))
		})
	})
})

var _ = Describe("TestMapReduceSummary", func() {
	var (
		loFriday    = &Friday{}
		summaryType = summary.MapReduce
		elements    = []models.Element{}
		file        = models.File{}
	)

	BeforeEach(func() {
		loFriday.Log = logger.NewLogger("test-mapreduce-summary")
		loFriday.LLM = FakeSummaryLLM{}
		loFriday.LimitToken = 50
		loFriday.Spliter = spliter.NewTextSpliter(loFriday.Log, 8, 2, "\n")
		elements = []models.Element{{
			Content: "test-content",
			Metadata: models.Metadata{
				Source:    "test-source",
				Title:     "test-title",
				ParentDir: "/",
			},
		}}
		file = models.File{
			Name:    "test-file",
			Source:  "test-file-source",
			Content: "test file content",
		}
	})

	Context("summary", func() {
		It("summary should be succeed", func() {
			summary, err := loFriday.Summary(context.TODO(), elements, summaryType)
			Expect(err).Should(BeNil())
			Expect(summary).Should(Equal(map[string]string{
				"test-source": "a b c",
			}))
		})
		It("SummaryFromFile should be succeed", func() {
			summary, err := loFriday.SummaryFromFile(context.TODO(), file, summaryType)
			Expect(err).Should(BeNil())
			Expect(summary).Should(Equal(map[string]string{
				"test-file-source": "a b c",
			}))
		})
	})
})

var _ = Describe("TestRefineSummary", func() {
	var (
		loFriday    = &Friday{}
		summaryType = summary.Refine
		elements    = []models.Element{}
		file        = models.File{}
	)

	BeforeEach(func() {
		loFriday.Log = logger.NewLogger("test-refine-summary")
		loFriday.LLM = FakeSummaryLLM{}
		loFriday.Spliter = spliter.NewTextSpliter(loFriday.Log, spliter.DefaultChunkSize, spliter.DefaultChunkOverlap, "\n")
		elements = []models.Element{{
			Content: "test-content",
			Metadata: models.Metadata{
				Source:    "test-source",
				Title:     "test-title",
				ParentDir: "/",
			},
		}}
		file = models.File{
			Name:    "test-file",
			Source:  "test-file-source",
			Content: "test-file-content",
		}
	})

	Context("summary", func() {
		It("summary should be succeed", func() {
			_, _ = loFriday.Summary(context.TODO(), elements, summaryType)
			// todo
		})
		It("SummaryFromFile should be succeed", func() {
			_, _ = loFriday.SummaryFromFile(context.TODO(), file, summaryType)
			// todo
		})
	})
})

type FakeSummaryLLM struct{}

var _ llm.LLM = &FakeSummaryLLM{}

func (f FakeSummaryLLM) Completion(ctx context.Context, prompt prompts.PromptTemplate, parameters map[string]string) ([]string, error) {
	return []string{}, nil
}

func (f FakeSummaryLLM) Chat(ctx context.Context, prompt prompts.PromptTemplate, parameters map[string]string) ([]string, error) {
	return []string{"a b c"}, nil
}
