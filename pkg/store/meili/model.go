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
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/meilisearch/meilisearch-go"

	"github.com/basenana/friday/pkg/models/doc"
)

var (
	DocFilterableAttrs     = []string{"namespace", "id", "entryId", "kind", "name", "source", "webUrl", "createdAt", "updatedAt"}
	DocSortAttrs           = []string{"createdAt", "updatedAt", "name"}
	DocAttrFilterableAttrs = []string{"namespace", "entryId", "key", "id", "kind", "value"}
	DocAttrSortAttrs       = []string{"createdAt", "updatedAt"}
)

type DocPtrInterface interface {
	ID() string
	EntryID() string
	Type() string
	String() string
}

type Document struct {
	Id        string `json:"id"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	EntryId   string `json:"entryId"`
	Name      string `json:"name"`
	Source    string `json:"source,omitempty"`
	WebUrl    string `json:"webUrl,omitempty"`

	Content     string `json:"content"`
	Summary     string `json:"summary,omitempty"`
	HeaderImage string `json:"headerImage,omitempty"`
	SubContent  string `json:"subContent,omitempty"`

	CreatedAt int64 `json:"createdAt,omitempty"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`
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

func (d *Document) String() string {
	return fmt.Sprintf("EntryId(%s) %s", d.EntryId, d.Name)
}

func (d *Document) FromModel(doc *doc.Document) *Document {
	d.Id = uuid.New().String()
	d.Kind = "document"
	d.Namespace = doc.Namespace
	d.EntryId = fmt.Sprintf("%d", doc.EntryId)
	d.Name = doc.Name
	d.Source = doc.Source
	d.WebUrl = doc.WebUrl
	d.Content = doc.Content
	d.Summary = doc.Summary
	d.HeaderImage = doc.HeaderImage
	d.SubContent = doc.SubContent
	d.CreatedAt = doc.CreatedAt.Unix()
	d.UpdatedAt = doc.ChangedAt.Unix()
	return d
}

func (d *Document) ToModel(attrs []*DocumentAttr) *doc.Document {
	entryId, _ := strconv.Atoi(d.EntryId)
	m := &doc.Document{
		EntryId:       int64(entryId),
		Name:          d.Name,
		Namespace:     d.Namespace,
		ParentEntryID: nil,
		Source:        d.Source,
		Content:       d.Content,
		Summary:       d.Summary,
		WebUrl:        d.WebUrl,
		HeaderImage:   d.HeaderImage,
		SubContent:    d.SubContent,
		Marked:        nil,
		Unread:        nil,
		CreatedAt:     time.Unix(d.CreatedAt, 0),
		ChangedAt:     time.Unix(d.UpdatedAt, 0),
	}

	for _, attr := range attrs {
		switch attr.Key {
		case "parentId":
			parentID, _ := strconv.Atoi(attr.Value.(string))
			pId := int64(parentID)
			m.ParentEntryID = &pId
		case "mark":
			m.Marked = attr.Value.(*bool)
		case "unRead":
			m.Unread = attr.Value.(*bool)
		}
	}
	return m
}

type DocumentList []*Document

func (d DocumentList) String() string {
	result := ""
	for _, doc := range d {
		result += fmt.Sprintf("EntryId(%s) %s\n", doc.EntryId, doc.Name)
	}
	return result
}

var _ DocPtrInterface = &Document{}

type DocumentAttr struct {
	Id        string      `json:"id"`
	Kind      string      `json:"kind"`
	Namespace string      `json:"namespace"`
	EntryId   string      `json:"entryId"`
	Key       string      `json:"key"`
	Value     interface{} `json:"value"`
}

var _ DocPtrInterface = &DocumentAttr{}

func (d *DocumentAttr) ID() string {
	return d.Id
}

func (d *DocumentAttr) EntryID() string {
	return d.EntryId
}

func (d *DocumentAttr) Type() string {
	return "attr"
}

func (d *DocumentAttr) String() string {
	return fmt.Sprintf("EntryId(%s) %s: %v", d.EntryId, d.Key, d.Value)
}

type DocumentAttrList []*DocumentAttr

func (d *DocumentAttrList) String() string {
	result := ""
	for _, attr := range *d {
		result += fmt.Sprintf("EntryId(%s) %s: %v\n", attr.EntryId, attr.Key, attr.Value)
	}
	return result
}

func (d *DocumentAttrList) FromModel(doc *doc.Document) *DocumentAttrList {
	attrs := make([]*DocumentAttr, 0)
	if doc.ParentEntryID != nil {
		attrs = append(attrs, &DocumentAttr{
			Id:        uuid.New().String(),
			Kind:      "attr",
			Namespace: doc.Namespace,
			EntryId:   fmt.Sprintf("%d", doc.EntryId),
			Key:       "parentId",
			Value:     doc.ParentEntryID,
		})
	}
	if doc.Marked != nil {
		attrs = append(attrs, &DocumentAttr{
			Id:        uuid.New().String(),
			Kind:      "attr",
			Namespace: doc.Namespace,
			EntryId:   fmt.Sprintf("%d", doc.EntryId),
			Key:       "mark",
			Value:     doc.Marked,
		})
	}
	if doc.Unread != nil {
		attrs = append(attrs, &DocumentAttr{
			Id:        uuid.New().String(),
			Kind:      "attr",
			Namespace: doc.Namespace,
			EntryId:   fmt.Sprintf("%d", doc.EntryId),
			Key:       "unRead",
			Value:     doc.Unread,
		})
	}
	return (*DocumentAttrList)(&attrs)
}

type DocumentQuery struct {
	AttrQueries []*AttrQuery

	Search      string
	HitsPerPage int64
	Page        int64
	Offset      int64
	Limit       int64
	Sort        []Sort
}

func (q *DocumentQuery) OfEntryId(namespace string, entryId int64) *DocumentQuery {
	return &DocumentQuery{
		AttrQueries: []*AttrQuery{
			{
				Attr:   "namespace",
				Option: "=",
				Value:  namespace,
			},
			{
				Attr:   "entryId",
				Option: "=",
				Value:  fmt.Sprintf("%d", entryId),
			},
			{
				Attr:   "kind",
				Option: "=",
				Value:  "document",
			},
		},
		Search:      "",
		HitsPerPage: 1,
		Page:        1,
	}
}

func (q *DocumentQuery) FromModel(query *doc.DocumentFilter) *DocumentQuery {
	q.Search = query.Search
	q.HitsPerPage = query.PageSize
	q.Page = query.Page
	q.Sort = []Sort{}
	if query.Order.Order == doc.Name {
		q.Sort = append(q.Sort, Sort{
			Attr: "name",
			Asc:  !query.Order.Desc,
		})
	}
	if query.Order.Order == doc.CreatedAt {
		q.Sort = append(q.Sort, Sort{
			Attr: "createdAt",
			Asc:  !query.Order.Desc,
		})
	}
	q.AttrQueries = []*AttrQuery{{
		Attr:   "kind",
		Option: "=",
		Value:  "document",
	}}
	if query.Namespace != "" {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "namespace",
			Option: "=",
			Value:  query.Namespace,
		})
	}
	if query.FuzzyName != "" {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "name",
			Option: "CONTAINS",
			Value:  query.FuzzyName,
		})
	}
	if query.Source != "" {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "source",
			Option: "=",
			Value:  query.Source,
		})
	}
	if query.CreatedAtStart != nil {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "createdAt",
			Option: ">=",
			Value:  query.CreatedAtStart.Unix(),
		})
	}
	if query.CreatedAtEnd != nil {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "createdAt",
			Option: "<=",
			Value:  query.CreatedAtEnd.Unix(),
		})
	}
	if query.ChangedAtStart != nil {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "updatedAt",
			Option: ">=",
			Value:  query.ChangedAtStart.Unix(),
		})
	}
	if query.ChangedAtEnd != nil {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "updatedAt",
			Option: "<=",
			Value:  query.ChangedAtEnd.Unix(),
		})
	}
	return q
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
	AttrQueries []*AttrQuery
}

func (q *DocumentAttrQuery) String() string {
	result := ""
	for _, aq := range q.AttrQueries {
		result += aq.String() + " "
	}
	return result
}

func (q *DocumentAttrQuery) FromModel(doc *doc.Document) *DocumentAttrQuery {
	q.AttrQueries = []*AttrQuery{{
		Attr:   "kind",
		Option: "=",
		Value:  "attr",
	}, {
		Attr:   "namespace",
		Option: "=",
		Value:  doc.Namespace,
	}}
	if doc.ParentEntryID != nil {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "key",
			Option: "=",
			Value:  "parentId",
		})
	}
	if doc.Marked != nil {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "key",
			Option: "=",
			Value:  "mark",
		})
	}
	if doc.Unread != nil {
		q.AttrQueries = append(q.AttrQueries, &AttrQuery{
			Attr:   "key",
			Option: "=",
			Value:  "unRead",
		})
	}
	return q
}

func (q *DocumentAttrQuery) OfEntryId(namespace, entryId string) *DocumentAttrQuery {
	q.AttrQueries = []*AttrQuery{
		{
			Attr:   "namespace",
			Option: "=",
			Value:  namespace,
		},
		{
			Attr:   "entryId",
			Option: "=",
			Value:  entryId,
		},
		{
			Attr:   "kind",
			Option: "=",
			Value:  "attr",
		},
	}
	return q
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

type DocumentAttrQueries []*DocumentAttrQuery

func (q *DocumentAttrQueries) FromFilter(query *doc.DocumentFilter) *DocumentAttrQueries {
	attrQueries := make([]*DocumentAttrQuery, 0)
	if query.ParentID != nil {
		attrQueries = append(attrQueries, &DocumentAttrQuery{
			AttrQueries: []*AttrQuery{
				{
					Attr:   "namespace",
					Option: "=",
					Value:  query.Namespace,
				},
				{
					Attr:   "kind",
					Option: "=",
					Value:  "attr",
				},
				{
					Attr:   "key",
					Option: "=",
					Value:  "parentId",
				},
				{
					Attr:   "value",
					Option: "=",
					Value:  query.ParentID,
				},
			},
		})
	}
	if query.Marked != nil {
		attrQueries = append(attrQueries, &DocumentAttrQuery{
			AttrQueries: []*AttrQuery{
				{
					Attr:   "namespace",
					Option: "=",
					Value:  query.Namespace,
				},
				{
					Attr:   "kind",
					Option: "=",
					Value:  "attr",
				},
				{
					Attr:   "key",
					Option: "=",
					Value:  "mark",
				},
				{
					Attr:   "value",
					Option: "=",
					Value:  query.Marked,
				},
			},
		})
	}
	if query.Unread != nil {
		attrQueries = append(attrQueries, &DocumentAttrQuery{
			AttrQueries: []*AttrQuery{
				{
					Attr:   "namespace",
					Option: "=",
					Value:  query.Namespace,
				},
				{
					Attr:   "kind",
					Option: "=",
					Value:  "attr",
				},
				{
					Attr:   "key",
					Option: "=",
					Value:  "unRead",
				},
				{
					Attr:   "value",
					Option: "=",
					Value:  query.Unread,
				},
			},
		})
	}
	return (*DocumentAttrQueries)(&attrQueries)
}

func (q *DocumentAttrQueries) String() string {
	result := ""
	for _, attrQuery := range *q {
		result += attrQuery.String() + " "
	}
	return result
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

func (aq *AttrQuery) String() string {
	return aq.ToFilter().(string)
}

func (q *DocumentQuery) String() string {
	filters := ""
	for _, aq := range q.AttrQueries {
		filters += aq.String() + " "
	}
	return fmt.Sprintf("search: [%s], attr query: [%s]", q.Search, filters)
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
