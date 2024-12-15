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
	"time"

	"github.com/basenana/friday/pkg/models/doc"
)

type DocRequest struct {
	doc.Document
}

func (r *DocRequest) Valid() error {
	if r.EntryId == 0 {
		return fmt.Errorf("entryId is required")
	}
	if r.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	return nil
}

type DocUpdateRequest struct {
	Namespace string `json:"namespace"`
	EntryId   int64  `json:"entryId,omitempty"`
	ParentID  *int64 `json:"parentId,omitempty"`
	UnRead    *bool  `json:"unRead,omitempty"`
	Mark      *bool  `json:"mark,omitempty"`
}

func (r *DocUpdateRequest) Valid() error {
	if r.EntryId == 0 {
		return fmt.Errorf("entryId is required")
	}
	if r.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	return nil
}

func (r *DocUpdateRequest) ToModel() *doc.Document {
	return &doc.Document{
		EntryId:       r.EntryId,
		Namespace:     r.Namespace,
		ParentEntryID: r.ParentID,
		Marked:        r.Mark,
		Unread:        r.UnRead,
	}
}

type DocQuery struct {
	EntryIds       []int64    `json:"entryIds,omitempty"`
	Namespace      string     `json:"namespace"`
	Source         string     `json:"source,omitempty"`
	WebUrl         string     `json:"webUrl,omitempty"`
	ParentID       *int64     `json:"parentId,omitempty"`
	UnRead         *bool      `json:"unRead,omitempty"`
	Mark           *bool      `json:"mark,omitempty"`
	CreatedAtStart *time.Time `json:"createdAtStart,omitempty"`
	CreatedAtEnd   *time.Time `json:"createdAtEnd,omitempty"`
	ChangedAtStart *time.Time `json:"changedAtStart,omitempty"`
	ChangedAtEnd   *time.Time `json:"changedAtEnd,omitempty"`
	FuzzyName      string     `json:"fuzzyName,omitempty"`

	Search string `json:"search"`

	PageSize int64 `json:"PageSize,omitempty"`
	Page     int64 `json:"page,omitempty"`
	Sort     int   `json:"sort,omitempty"`
	Desc     bool  `json:"desc,omitempty"`
}

func (q *DocQuery) ToModel() *doc.DocumentFilter {
	return &doc.DocumentFilter{
		Namespace:      q.Namespace,
		Search:         q.Search,
		FuzzyName:      q.FuzzyName,
		ParentID:       q.ParentID,
		Source:         q.Source,
		Marked:         q.Mark,
		Unread:         q.UnRead,
		CreatedAtStart: q.CreatedAtStart,
		CreatedAtEnd:   q.CreatedAtEnd,
		ChangedAtStart: q.ChangedAtStart,
		ChangedAtEnd:   q.ChangedAtEnd,
		Page:           q.Page,
		PageSize:       q.PageSize,
		Order: doc.DocumentOrder{
			Order: doc.DocOrder(q.Sort),
			Desc:  q.Desc,
		},
	}
}
