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

package doc

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/meilisearch/meilisearch-go"
)

var (
	DocFilterableAttrs     = []string{"namespace", "id", "entryId", "name", "source", "webUrl", "createdAt", "updatedAt"}
	DocAttrFilterableAttrs = []string{"namespace", "entryId", "key", "id", "value"}
	DocSortAttrs           = []string{"createdAt", "updatedAt"}
)

type DocPtrInterface interface {
	ID() string
	EntryID() string
	Type() string
}

type Document struct {
	Id        string `json:"id"`
	Namespace string `json:"namespace"`
	EntryId   string `json:"entryId"`
	Name      string `json:"name"`
	Source    string `json:"source,omitempty"`
	WebUrl    string `json:"webUrl,omitempty"`

	Content     string `json:"content"`
	Summary     string `json:"summary,omitempty"`
	HeaderImage string `json:"headerImage,omitempty"`
	SubContent  string `json:"subContent,omitempty"`

	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

func (d *Document) ID() string {
	return d.Id
}

func (d *Document) EntryID() string {
	return d.EntryId
}

func (d *Document) Type() string {
	return "document"
}

type DocumentAttr struct {
	Id        string      `json:"id"`
	Namespace string      `json:"namespace"`
	EntryId   string      `json:"entryId"`
	Key       string      `json:"key"`
	Value     interface{} `json:"value"`
}

func (d *DocumentAttr) ID() string {
	return d.Id
}

func (d *DocumentAttr) EntryID() string {
	return d.EntryId
}

func (d *DocumentAttr) Type() string {
	return "attr"
}

var _ DocPtrInterface = &Document{}
var _ DocPtrInterface = &DocumentAttr{}

type DocumentQuery struct {
	AttrQueries []AttrQuery

	Search      string
	HitsPerPage int64
	Page        int64
	Offset      int64
	Limit       int64
	Sort        []Sort
}

type Sort struct {
	Attr string
	Asc  bool
}

func (s *Sort) String() string {
	if s.Asc {
		return fmt.Sprintf("%s:asc", s.Attr)
	}
	return fmt.Sprintf("%s:desc", s.Attr)
}

type DocumentAttrQuery struct {
	AttrQueries []AttrQuery
}

type AttrQuery struct {
	Attr   string
	Option string
	Value  interface{}
}

func (aq *AttrQuery) ToFilter() interface{} {
	vs, _ := json.Marshal(aq.Value)
	return fmt.Sprintf("%s %s %s", aq.Attr, aq.Option, vs)
}

func (q *DocumentQuery) ToRequest() *meilisearch.SearchRequest {
	// build filter
	filter := []interface{}{}
	for _, aq := range q.AttrQueries {
		filter = append(filter, aq.ToFilter())
	}
	sorts := []string{}
	for _, s := range q.Sort {
		sorts = append(sorts, s.String())
	}

	return &meilisearch.SearchRequest{
		Offset:      q.Offset,
		Limit:       q.Limit,
		Sort:        sorts,
		HitsPerPage: q.HitsPerPage,
		Page:        q.Page,
		Query:       q.Search,
		Filter:      filter,
	}
}

func (q *DocumentAttrQuery) ToRequest() *meilisearch.SearchRequest {
	filter := []interface{}{}
	for _, aq := range q.AttrQueries {
		filter = append(filter, aq.ToFilter())
	}
	return &meilisearch.SearchRequest{
		Filter:      filter,
		Limit:       10000,
		HitsPerPage: 10000,
		Query:       "",
	}
}
