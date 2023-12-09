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
	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/spliter"
	"github.com/basenana/friday/pkg/utils/logger"
	"github.com/basenana/friday/pkg/vectorstore"
)

var _ = Describe("TestIngest", func() {
	var (
		loFriday = &Friday{}
	)

	BeforeEach(func() {
		loFriday.Vector = FakeStore{}
		loFriday.Log = logger.NewLogger("test-ingest")
		loFriday.Spliter = spliter.NewTextSpliter(loFriday.Log, spliter.DefaultChunkSize, spliter.DefaultChunkOverlap, "\n")
		loFriday.Embedding = FakeEmbedding{}
	})

	Context("ingest", func() {
		It("ingest should be succeed", func() {
			elements := []models.Element{
				{
					Content: "test-content",
					Name:    "test-title",
					Group:   0,
				},
			}
			_, err := loFriday.Ingest(context.TODO(), elements)
			Expect(err).Should(BeNil())
		})
	})
})

type FakeStore struct{}

var _ vectorstore.VectorStore = &FakeStore{}

func (f FakeStore) Store(ctx context.Context, element *models.Element, extra map[string]any) error {
	return nil
}

func (f FakeStore) Search(ctx context.Context, parentId int64, vectors []float32, k int) ([]*models.Doc, error) {
	return []*models.Doc{}, nil
}

func (f FakeStore) Get(ctx context.Context, oid int64, name string, group int) (*models.Element, error) {
	return &models.Element{}, nil
}

type FakeEmbedding struct{}

var _ embedding.Embedding = FakeEmbedding{}

func (f FakeEmbedding) VectorQuery(ctx context.Context, doc string) ([]float32, map[string]interface{}, error) {
	return []float32{}, map[string]interface{}{}, nil
}

func (f FakeEmbedding) VectorDocs(ctx context.Context, docs []string) ([][]float32, []map[string]interface{}, error) {
	return [][]float32{}, []map[string]interface{}{}, nil
}
