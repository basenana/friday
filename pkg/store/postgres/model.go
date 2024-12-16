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

package postgres

import (
	"encoding/json"
	"time"

	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/models/vector"
)

type Index struct {
	ID        string `gorm:"column:id;type:varchar(256);primaryKey"`
	Name      string `gorm:"column:name;index:index_name"`
	Namespace string `gorm:"column:namespace;index:index_namespace"`
	OID       int64  `gorm:"column:oid;index:index_oid"`
	Group     int    `gorm:"column:idx_group;index:index_group"`
	ParentID  int64  `gorm:"column:parent_entry_id;index:index_parent_id"`
	Content   string `gorm:"column:content"`
	Vector    string `gorm:"column:vector;type:json"`
	Extra     string `gorm:"column:extra"`
	CreatedAt int64  `gorm:"column:created_at"`
	ChangedAt int64  `gorm:"column:changed_at"`
}

func (v *Index) TableName() string {
	return "friday_idx"
}

func (v *Index) Update(vector *Index) {
	v.ID = vector.ID
	v.Name = vector.Name
	v.OID = vector.OID
	v.Group = vector.Group
	v.ParentID = vector.ParentID
	v.Content = vector.Content
	v.Extra = vector.Extra
	v.Vector = vector.Vector
	v.ChangedAt = time.Now().UnixNano()
}

func (v *Index) From(element *vector.Element) (*Index, error) {
	i := &Index{
		ID:       element.ID,
		Name:     element.Name,
		OID:      element.OID,
		Group:    element.Group,
		ParentID: element.ParentId,
		Content:  element.Content,
	}
	vector, err := json.Marshal(element.Vector)
	if err != nil {
		return nil, err
	}
	i.Vector = string(vector)

	return i, nil
}

func (v *Index) To() (*vector.Element, error) {
	res := &vector.Element{
		ID:       v.ID,
		Name:     v.Name,
		Group:    v.Group,
		OID:      v.OID,
		ParentId: v.ParentID,
		Content:  v.Content,
	}
	var vector []float32
	err := json.Unmarshal([]byte(v.Vector), &vector)
	if err != nil {
		return nil, err
	}
	res.Vector = vector

	return res, nil
}

func (v *Index) ToDoc() *vector.Doc {
	res := &vector.Doc{
		Id:       v.ID,
		OID:      v.OID,
		Name:     v.Name,
		Group:    v.Group,
		Content:  v.Content,
		ParentId: v.ParentID,
	}

	return res
}

type BleveKV struct {
	ID    string `gorm:"column:id;primaryKey"`
	Key   []byte `gorm:"column:key"`
	Value []byte `gorm:"column:value"`
}

func (v *BleveKV) TableName() string {
	return "friday_blevekv"
}

type Document struct {
	ID            int64     `gorm:"column:id;primaryKey"`
	OID           int64     `gorm:"column:oid;index:doc_oid"`
	Name          string    `gorm:"column:name;index:doc_name"`
	Namespace     string    `gorm:"column:namespace;index:doc_ns"`
	Source        string    `gorm:"column:source;index:doc_source"`
	ParentEntryID *int64    `gorm:"column:parent_entry_id;index:doc_parent_entry_id"`
	Keywords      string    `gorm:"column:keywords"`
	Content       string    `gorm:"column:content"`
	Summary       string    `gorm:"column:summary"`
	HeaderImage   string    `gorm:"column:header_image"`
	SubContent    string    `gorm:"column:sub_content"`
	Marked        bool      `gorm:"column:marked;index:doc_is_marked"`
	Unread        bool      `gorm:"column:unread;index:doc_is_unread"`
	CreatedAt     time.Time `gorm:"column:created_at"`
	ChangedAt     time.Time `gorm:"column:changed_at"`
}

func (d *Document) TableName() string {
	return "document"
}

func (d *Document) From(document *doc.Document) *Document {
	d.ID = document.EntryId
	d.OID = document.EntryId
	d.Name = document.Name
	d.Namespace = document.Namespace
	d.ParentEntryID = document.ParentEntryID
	d.Source = document.Source
	d.Content = document.Content
	d.Summary = document.Summary
	d.CreatedAt = document.CreatedAt
	d.ChangedAt = document.ChangedAt
	if document.Marked != nil {
		d.Marked = *document.Marked
	}
	if document.Unread != nil {
		d.Unread = *document.Unread
	}
	d.HeaderImage = document.HeaderImage
	d.SubContent = document.SubContent
	return d
}

func (d *Document) UpdateFrom(document *doc.Document) *Document {
	if document.Unread != nil {
		d.Unread = *document.Unread
	}
	if document.Marked != nil {
		d.Marked = *document.Marked
	}
	if document.ParentEntryID != nil {
		d.ParentEntryID = document.ParentEntryID
	}
	return d
}

func (d *Document) To() *doc.Document {
	result := &doc.Document{
		EntryId:       d.OID,
		Name:          d.Name,
		Namespace:     d.Namespace,
		ParentEntryID: d.ParentEntryID,
		Source:        d.Source,
		Content:       d.Content,
		Summary:       d.Summary,
		SubContent:    d.SubContent,
		HeaderImage:   d.HeaderImage,
		Marked:        &d.Marked,
		Unread:        &d.Unread,
		CreatedAt:     d.CreatedAt,
		ChangedAt:     d.ChangedAt,
	}
	return result
}
