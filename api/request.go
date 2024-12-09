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

package api

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/basenana/friday/pkg/models/doc"
)

type DocRequest struct {
	EntryId   string `json:"entryId,omitempty"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Source    string `json:"source,omitempty"`
	WebUrl    string `json:"webUrl,omitempty"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	ChangedAt int64  `json:"changedAt,omitempty"`
}

func (r *DocRequest) ToDocument() *doc.Document {
	return &doc.Document{
		Id:        uuid.New().String(),
		EntryId:   r.EntryId,
		Name:      r.Name,
		Kind:      "document",
		Namespace: r.Namespace,
		Source:    r.Source,
		WebUrl:    r.WebUrl,
		Content:   r.Content,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.ChangedAt,
	}
}

func (r *DocRequest) Valid() error {
	if r.EntryId == "" || r.EntryId == "0" {
		return fmt.Errorf("entryId is required")
	}
	if r.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	return nil
}

type DocAttrRequest struct {
	Namespace string `json:"namespace"`
	EntryId   string `json:"entryId,omitempty"`
	ParentID  string `json:"parentId,omitempty"`
	UnRead    *bool  `json:"unRead,omitempty"`
	Mark      *bool  `json:"mark,omitempty"`
}

func (r *DocAttrRequest) ToDocAttr() []*doc.DocumentAttr {
	attrs := []*doc.DocumentAttr{}
	if r.ParentID != "" {
		attrs = append(attrs, &doc.DocumentAttr{
			Id:        uuid.New().String(),
			Namespace: r.Namespace,
			EntryId:   r.EntryId,
			Key:       "parentId",
			Value:     r.ParentID,
		})
	}
	if r.Mark != nil {
		attrs = append(attrs, &doc.DocumentAttr{
			Id:        uuid.New().String(),
			Namespace: r.Namespace,
			EntryId:   r.EntryId,
			Key:       "mark",
			Value:     *r.Mark,
		})
	}
	if r.UnRead != nil {
		attrs = append(attrs, &doc.DocumentAttr{
			Id:        uuid.New().String(),
			Namespace: r.Namespace,
			EntryId:   r.EntryId,
			Key:       "unRead",
			Value:     *r.UnRead,
		})

	}
	return attrs
}

type DocQuery struct {
	IDs            []string `json:"ids"`
	Namespace      string   `json:"namespace"`
	Source         string   `json:"source,omitempty"`
	WebUrl         string   `json:"webUrl,omitempty"`
	ParentID       string   `json:"parentId,omitempty"`
	UnRead         *bool    `json:"unRead,omitempty"`
	Mark           *bool    `json:"mark,omitempty"`
	CreatedAtStart *int64   `json:"createdAtStart,omitempty"`
	CreatedAtEnd   *int64   `json:"createdAtEnd,omitempty"`
	ChangedAtStart *int64   `json:"changedAtStart,omitempty"`
	ChangedAtEnd   *int64   `json:"changedAtEnd,omitempty"`
	FuzzyName      *string  `json:"fuzzyName,omitempty"`

	Search string `json:"search"`

	HitsPerPage int64  `json:"hitsPerPage,omitempty"`
	Page        int64  `json:"page,omitempty"`
	Limit       int64  `json:"limit,omitempty"`
	Sort        string `json:"sort,omitempty"`
	Desc        bool   `json:"desc,omitempty"`
}

func (q *DocQuery) ToQuery() *doc.DocumentQuery {
	query := &doc.DocumentQuery{
		Search:      q.Search,
		HitsPerPage: q.HitsPerPage,
		Page:        q.Page,
		Sort: []doc.Sort{{
			Attr: q.Sort,
			Asc:  !q.Desc,
		}},
	}
	attrQueries := []*doc.AttrQuery{{
		Attr:   "namespace",
		Option: "=",
		Value:  q.Namespace,
	}}
	if q.Source != "" {
		attrQueries = append(attrQueries, &doc.AttrQuery{
			Attr:   "source",
			Option: "=",
			Value:  q.Source,
		})
	}
	if q.WebUrl != "" {
		attrQueries = append(attrQueries, &doc.AttrQuery{
			Attr:   "webUrl",
			Option: "=",
			Value:  q.WebUrl,
		})
	}
	if q.CreatedAtStart != nil {
		attrQueries = append(attrQueries, &doc.AttrQuery{
			Attr:   "createdAt",
			Option: ">=",
			Value:  *q.CreatedAtStart,
		})
	}
	if q.ChangedAtStart != nil {
		attrQueries = append(attrQueries, &doc.AttrQuery{
			Attr:   "updatedAt",
			Option: ">=",
			Value:  *q.ChangedAtStart,
		})
	}
	if q.CreatedAtEnd != nil {
		attrQueries = append(attrQueries, &doc.AttrQuery{
			Attr:   "createdAt",
			Option: "<=",
			Value:  *q.CreatedAtEnd,
		})
	}
	if q.ChangedAtEnd != nil {
		attrQueries = append(attrQueries, &doc.AttrQuery{
			Attr:   "updatedAt",
			Option: "<=",
			Value:  *q.ChangedAtEnd,
		})
	}
	if q.FuzzyName != nil {
		attrQueries = append(attrQueries, &doc.AttrQuery{
			Attr:   "name",
			Option: "CONTAINS",
			Value:  *q.FuzzyName,
		})
	}

	query.AttrQueries = attrQueries
	return query
}

func (q *DocQuery) GetAttrQueries() []*doc.DocumentAttrQuery {
	attrQueries := []*doc.DocumentAttrQuery{}
	if q.UnRead != nil {
		attrQueries = append(attrQueries, &doc.DocumentAttrQuery{
			AttrQueries: []*doc.AttrQuery{
				{
					Attr:   "namespace",
					Option: "=",
					Value:  q.Namespace,
				},
				{
					Attr:   "key",
					Option: "=",
					Value:  "unRead",
				},
				{
					Attr:   "value",
					Option: "=",
					Value:  *q.UnRead,
				},
			},
		})
	}
	if q.Mark != nil {
		attrQueries = append(attrQueries, &doc.DocumentAttrQuery{
			AttrQueries: []*doc.AttrQuery{
				{
					Attr:   "namespace",
					Option: "=",
					Value:  q.Namespace,
				},
				{
					Attr:   "key",
					Option: "=",
					Value:  "mark",
				},
				{
					Attr:   "value",
					Option: "=",
					Value:  *q.Mark,
				},
			},
		})
	}
	if q.ParentID != "" {
		attrQueries = append(attrQueries, &doc.DocumentAttrQuery{
			AttrQueries: []*doc.AttrQuery{
				{
					Attr:   "namespace",
					Option: "=",
					Value:  q.Namespace,
				},
				{
					Attr:   "key",
					Option: "=",
					Value:  "parentId",
				},
				{
					Attr:   "value",
					Option: "=",
					Value:  q.ParentID,
				},
			},
		})
	}
	return attrQueries
}

type DocumentWithAttr struct {
	*doc.Document

	ParentID string `json:"parentId,omitempty"`
	UnRead   *bool  `json:"unRead,omitempty"`
	Mark     *bool  `json:"mark,omitempty"`
}
