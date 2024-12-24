/*
 Copyright 2024 Friday Author.

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

package store

import (
	"context"

	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/models/vector"
)

type VectorStore interface {
	Store(ctx context.Context, element *vector.Element, extra map[string]any) error
	Search(ctx context.Context, query vector.VectorDocQuery, vectors []float32, k int) ([]*vector.Doc, error)
	Get(ctx context.Context, oid int64, name string, group int) (*vector.Element, error)
}

type DocStoreInterface interface {
	CreateDocument(ctx context.Context, doc *doc.Document) error
	UpdateTokens(ctx context.Context, doc *doc.Document) error
	UpdateDocument(ctx context.Context, doc *doc.Document) error
	GetDocument(ctx context.Context, entryId int64) (*doc.Document, error)
	FilterDocuments(ctx context.Context, filter *doc.DocumentFilter) ([]*doc.Document, error)
	DeleteDocument(ctx context.Context, docId int64) error
}
