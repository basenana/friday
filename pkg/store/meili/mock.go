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

package meili

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/store"
)

type MockClient struct {
	docs  []*Document
	attrs []*DocumentAttr
}

var _ store.DocStoreInterface = &MockClient{}

func (m *MockClient) CreateDocument(ctx context.Context, doc *doc.Document) error {
	m.docs = append(m.docs, (&Document{}).FromModel(doc))
	newAttrs := (&DocumentAttrList{}).FromModel(doc)
	m.attrs = append(m.attrs, *newAttrs...)
	return nil
}

func (m *MockClient) UpdateTokens(ctx context.Context, doc *doc.Document) error {
	return nil
}

func (m *MockClient) UpdateDocument(ctx context.Context, doc *doc.Document) error {
	aq := (&DocumentAttrQuery{}).FromModel(doc)
	result := []*DocumentAttr{}
	for _, attr := range m.attrs {
		matched := true
		all := len(aq.AttrQueries)

		for _, q := range aq.AttrQueries {
			if q.Attr == "namespace" {
				all -= 1
				if !match(q, attr.Namespace) {
					matched = false
					continue
				}
			}
			if q.Attr == "entryId" {
				all -= 1
				if !match(q, attr.EntryId) {
					matched = false
					continue
				}
			}
			if attr.Key == q.Attr {
				all -= 1
				if !match(q, attr.Value) {
					matched = false
				}
			}
			if q.Attr == "kind" {
				all -= 1
				if !match(q, attr.Kind) {
					matched = false
					continue
				}
			}
		}
		if matched && all == 0 {
			result = append(result, attr)
		}
	}
	return nil
}

func (m *MockClient) GetDocument(ctx context.Context, entryId int64) (*doc.Document, error) {
	var res *Document
	for _, d := range m.docs {
		if d.EntryId == fmt.Sprintf("%d", entryId) {
			res = d
			break
		}
	}
	attrs := make([]*DocumentAttr, 0)
	for _, attr := range m.attrs {
		if attr.EntryId == fmt.Sprintf("%d", entryId) {
			attrs = append(attrs, attr)
		}
	}
	if res != nil {
		return res.ToModel(attrs), nil
	}
	return nil, nil
}

func (m *MockClient) FilterDocuments(ctx context.Context, filter *doc.DocumentFilter) ([]*doc.Document, error) {
	query := (&DocumentQuery{}).FromModel(filter)
	if filter.ParentID != nil || filter.Unread != nil || filter.Marked != nil {
		attrQuery := (&DocumentAttrQueries{}).FromFilter(filter)
		entryId := make([]string, 0)
		for _, aq := range *attrQuery {
			for _, attr := range m.attrs {
				all := len(aq.AttrQueries)
				matched := true
				for _, q := range aq.AttrQueries {
					switch q.Attr {
					case "entryId":
						all -= 1
						if !match(q, attr.EntryId) {
							matched = false
						}
					case "namespace":
						all -= 1
						if !match(q, attr.Namespace) {
							matched = false
						}
					case "key":
						all -= 1
						if !match(q, attr.Key) {
							matched = false
						}
					case "value":
						all -= 1
						if !match(q, attr.Value) {
							matched = false
						}
					case "kind":
						all -= 1
						if !match(q, attr.Kind) {
							matched = false
						}
					}
				}
				if matched && all == 0 {
					entryId = append(entryId, attr.EntryId)
				}
			}
		}
		if len(entryId) != 0 {
			query.AttrQueries = append(query.AttrQueries, &AttrQuery{
				Attr:   "entryId",
				Option: "IN",
				Value:  entryId,
			})
		} else {
			return nil, nil
		}
	}

	result := []*doc.Document{}
	for _, d := range m.docs {
		matched := true
		all := len(query.AttrQueries)
		for _, q := range query.AttrQueries {
			switch q.Attr {
			case "entryId":
				all -= 1
				if !match(q, d.EntryId) {
					matched = false
				}
			case "namespace":
				all -= 1
				if !match(q, d.Namespace) {
					matched = false
				}
			case "id":
				all -= 1
				if !match(q, d.Id) {
					matched = false
				}
			case "kind":
				all -= 1
				if !match(q, d.Kind) {
					matched = false
				}
			}
		}
		if matched && all == 0 && strings.Contains(d.Content, query.Search) {
			result = append(result, d.ToModel(nil))
		}
	}
	return result, nil
}

func (m *MockClient) DeleteDocument(ctx context.Context, docId int64) error {
	for i, d := range m.docs {
		if d.EntryId == fmt.Sprintf("%d", docId) {
			m.docs = append(m.docs[:i], m.docs[i+1:]...)
			break
		}
	}
	for i, attr := range m.attrs {
		if attr.EntryId == fmt.Sprintf("%d", docId) {
			m.attrs = append(m.attrs[:i], m.attrs[i+1:]...)
			break
		}
	}
	return nil
}

func match[T string | interface{}](aq *AttrQuery, t T) bool {
	if aq.Option == "=" && reflect.DeepEqual(t, aq.Value.(T)) {
		return true
	}
	if aq.Option == "IN" {
		value := aq.Value.([]T)
		for _, v := range value {
			if reflect.DeepEqual(t, v) {
				return true
			}
		}
	}
	return false
}
