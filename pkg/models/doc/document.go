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
	"fmt"
	"time"

	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/utils"
)

type Document struct {
	EntryId       int64     `json:"entry_id"`
	Name          string    `json:"name"`
	Namespace     string    `json:"namespace"`
	ParentEntryID *int64    `json:"parent_entry_id"`
	Source        string    `json:"source"`
	Content       string    `json:"content,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	WebUrl        string    `json:"web_url,omitempty"`
	HeaderImage   string    `json:"header_image,omitempty"`
	SubContent    string    `json:"sub_content,omitempty"`
	PureContent   string    `json:"pure_content,omitempty"`
	TitleTokens   []string  `json:"title_tokens,omitempty"`
	ContentTokens []string  `json:"content_tokens,omitempty"`
	SearchContext []string  `json:"search_context,omitempty"`
	Marked        *bool     `json:"marked,omitempty"`
	Unread        *bool     `json:"unread,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ChangedAt     time.Time `json:"changed_at"`
}

func (d *Document) NewTest() *Document {
	return &Document{
		EntryId:       1,
		Name:          "test",
		Namespace:     models.DefaultNamespaceValue,
		ParentEntryID: utils.ToPtr(int64(1)),
		Source:        "test",
		Content:       "test",
		Summary:       "test",
		WebUrl:        "test",
		HeaderImage:   "test",
		SubContent:    "test",
		Marked:        utils.ToPtr(true),
		Unread:        utils.ToPtr(true),
		CreatedAt:     time.Now(),
		ChangedAt:     time.Now(),
	}
}

type DocumentFilter struct {
	Namespace      string
	Search         string
	FuzzyName      string
	ParentID       *int64
	Source         string
	Marked         *bool
	Unread         *bool
	CreatedAtStart *time.Time
	CreatedAtEnd   *time.Time
	ChangedAtStart *time.Time
	ChangedAtEnd   *time.Time

	// Pagination
	Page     int64
	PageSize int64
	Order    DocumentOrder
}

func (f *DocumentFilter) String() string {
	s := fmt.Sprintf("namespace: %s", f.Namespace)
	if f.Search != "" {
		s += fmt.Sprintf(", search: %s", f.Search)
	}
	if f.FuzzyName != "" {
		s += fmt.Sprintf(", fuzzyName: %s", f.FuzzyName)
	}
	if f.ParentID != nil {
		s += fmt.Sprintf(", parentID: %d", *f.ParentID)
	}
	if f.Source != "" {
		s += fmt.Sprintf(", source: %s", f.Source)
	}
	if f.Marked != nil {
		s += fmt.Sprintf(", marked: %v", *f.Marked)
	}
	if f.Unread != nil {
		s += fmt.Sprintf(", unread: %v", *f.Unread)
	}
	if f.CreatedAtStart != nil {
		s += fmt.Sprintf(", createdAtStart: %s", f.CreatedAtStart)
	}
	if f.CreatedAtEnd != nil {
		s += fmt.Sprintf(", createdAtEnd: %s", f.CreatedAtEnd)
	}
	if f.ChangedAtStart != nil {
		s += fmt.Sprintf(", changedAtStart: %s", f.ChangedAtStart)
	}
	if f.ChangedAtEnd != nil {
		s += fmt.Sprintf(", changedAtEnd: %s", f.ChangedAtEnd)
	}
	s += fmt.Sprintf(", page: %d, pageSize: %d, sort: %s", f.Page, f.PageSize, f.Order.String())
	return s
}

type DocumentOrder struct {
	Order DocOrder
	Desc  bool
}

func (o DocumentOrder) String() string {
	return fmt.Sprintf("order: %s, desc: %v", o.Order.String(), o.Desc)
}

type DocOrder int

const (
	Name DocOrder = iota
	Source
	Marked
	Unread
	CreatedAt
)

func (d DocOrder) String() string {
	names := []string{
		"name",
		"source",
		"marked",
		"unread",
		"created_at",
	}
	if d < Name || d > CreatedAt {
		return ""
	}
	return names[d]
}
