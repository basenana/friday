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

package docstore

import (
	"context"
	"reflect"
	"strings"

	"github.com/basenana/friday/pkg/models/doc"
)

type MockClient struct {
	docs  []doc.Document
	attrs []doc.DocumentAttr
}

var _ DocStoreInterface = &MockClient{}

func (m *MockClient) Store(ctx context.Context, docPtr doc.DocPtrInterface) error {
	if docPtr.Type() == "document" {
		d := docPtr.(*doc.Document)
		if m.docs == nil {
			m.docs = []doc.Document{}
		}
		m.docs = append(m.docs, *d)
	}
	if docPtr.Type() == "attr" {
		d := docPtr.(*doc.DocumentAttr)
		if m.attrs == nil {
			m.attrs = []doc.DocumentAttr{}
		}
		m.attrs = append(m.attrs, *d)
	}
	return nil
}

func (m *MockClient) FilterAttr(ctx context.Context, query *doc.DocumentAttrQuery) ([]doc.DocumentAttr, error) {
	aq := query.AttrQueries
	result := []doc.DocumentAttr{}
	for _, attr := range m.attrs {
		matched := true
		all := len(aq)
		for _, q := range aq {
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
	return result, nil
}

func (m *MockClient) Search(ctx context.Context, query *doc.DocumentQuery) ([]doc.Document, error) {
	aq := query.AttrQueries
	result := []doc.Document{}
	for _, d := range m.docs {
		matched := true
		all := len(aq)
		for _, q := range aq {
			if q.Attr == "entryId" {
				all -= 1
				if !match(q, d.EntryId) {
					matched = false
					continue
				}
			}
			if q.Attr == "namespace" {
				all -= 1
				if !match(q, d.Namespace) {
					matched = false
					continue
				}
			}
			if q.Attr == "id" {
				all -= 1
				if !match(q, d.Id) {
					matched = false
					continue
				}
			}
			if q.Attr == "kind" {
				all -= 1
				if !match(q, d.Kind) {
					matched = false
					continue
				}
			}
		}
		if matched && all == 0 && strings.Contains(d.Content, query.Search) {
			result = append(result, d)
		}
	}
	return result, nil
}

func (m *MockClient) DeleteByFilter(ctx context.Context, aqs []doc.AttrQuery) error {
	attrs := make(map[string]doc.DocumentAttr)
	for _, attr := range m.attrs {
		attrs[attr.Id] = attr
	}
	docs := make(map[string]doc.Document)
	for _, d := range m.docs {
		docs[d.Id] = d
	}
	for _, d := range m.docs {
		matched := true
		all := len(aqs)
		for _, q := range aqs {
			if q.Attr == "entryId" {
				all -= 1
				if !match(q, d.EntryId) {
					matched = false
					continue
				}
			}
			if q.Attr == "namespace" {
				all -= 1
				if !match(q, d.Namespace) {
					matched = false
					continue
				}
			}
			if q.Attr == "id" {
				all -= 1
				if !match(q, d.Id) {
					matched = false
					continue
				}
			}
			if q.Attr == "kind" {
				all -= 1
				if !match(q, d.Kind) {
					matched = false
					continue
				}
			}
		}
		if matched && all == 0 {
			delete(docs, d.Id)
		}
	}
	for _, attr := range m.attrs {
		matched := true
		all := len(aqs)
		for _, aq := range aqs {
			if attr.Key == aq.Attr {
				all -= 1
				if !match(aq, attr.Value) {
					matched = false
				}
			}
			if aq.Attr == "kind" {
				all -= 1
				if !match(aq, attr.Kind) {
					matched = false
					continue
				}
			}

		}
		if matched && all == 0 {
			delete(attrs, attr.Id)
		}
	}
	var attrsSlice []doc.DocumentAttr
	for _, v := range attrs {
		attrsSlice = append(attrsSlice, v)
	}
	m.attrs = attrsSlice
	var docsSlice []doc.Document
	for _, v := range docs {
		docsSlice = append(docsSlice, v)
	}
	m.docs = docsSlice
	return nil
}

func match[T string | interface{}](aq doc.AttrQuery, t T) bool {
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
