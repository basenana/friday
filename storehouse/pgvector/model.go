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

package pgvector

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/basenana/friday/types"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type SessionModel struct {
	ID        string `gorm:"column:id;primaryKey"`
	Type      string `gorm:"column:type"`
	Metadata  JSON   `gorm:"column:metadata"`
	System    string `gorm:"column:system"`
	Purpose   string `gorm:"column:purpose"`
	Summary   string `gorm:"column:summary"`
	Report    string `gorm:"column:report"`
	Feedback  string `gorm:"column:feedback"`
	CreatedAt int64  `gorm:"column:created_at"`
	ChangedAt int64  `gorm:"column:changed_at"`
}

func (s *SessionModel) TableName() string {
	return "friday_sessions"
}

func (s *SessionModel) From(session *types.Session) {
	s.ID = session.ID
	s.Type = string(session.Type)
	s.Metadata, _ = json.Marshal(session.Metadata)
	s.System = session.System
	s.Purpose = session.Purpose
	s.Summary = session.Summary
	s.Report = session.Report
	s.Feedback = session.Feedback
	s.ChangedAt = time.Now().UnixNano()
	if s.CreatedAt == 0 {
		s.CreatedAt = s.ChangedAt
	}
}

func (s *SessionModel) To() *types.Session {
	session := &types.Session{
		ID:       s.ID,
		Type:     types.SessionType(s.Type),
		Metadata: make(map[string]string),
		System:   s.System,
		Purpose:  s.Purpose,
		Summary:  s.Summary,
		Report:   s.Report,
		Feedback: s.Feedback,
	}

	jsonData(string(s.Metadata), &session.Metadata)
	return session
}

type MessageModel struct {
	ID        int64  `gorm:"column:id;primaryKey;autoIncrement"`
	SessionID string `gorm:"column:session_id;index:msg_session"`
	Content   JSON   `gorm:"column:content"`
	CreatedAt int64  `gorm:"column:created_at"`
	ChangedAt int64  `gorm:"column:changed_at"`
}

func (m *MessageModel) TableName() string {
	return "friday_messages"
}

func (m *MessageModel) From(sessionID string, msg *types.Message) {
	m.SessionID = sessionID
	m.Content, _ = json.Marshal(msg)
	m.ChangedAt = time.Now().UnixNano()
	if m.CreatedAt == 0 {
		m.CreatedAt = m.ChangedAt
	}
}

func (m *MessageModel) To() *types.Message {
	msg := &types.Message{}

	jsonData(string(m.Content), &msg)
	return msg
}

type MemoryModel struct {
	ID        string `gorm:"column:id;primaryKey"`
	Metadata  JSON   `gorm:"column:metadata"`
	Overview  string `gorm:"column:overview"`
	Details   string `gorm:"column:details"`
	Relevant  string `gorm:"column:relevant"`
	Comment   string `gorm:"column:comment"`
	CreatedAt int64  `gorm:"column:created_at"`
	ChangedAt int64  `gorm:"column:changed_at"`
}

func (m *MemoryModel) TableName() string {
	return "friday_memories"
}

func (m *MemoryModel) From(memory *types.Memory) {
	m.ID = memory.ID
	m.Metadata, _ = json.Marshal(memory.Metadata)
	m.Overview = memory.Overview
	m.Details = memory.Details
	m.Relevant = memory.Relevant
	m.Comment = memory.Comment
	if memory.Time.IsZero() {
		memory.Time = time.Now()
	}
	m.ChangedAt = memory.Time.UnixNano()
	if m.CreatedAt == 0 {
		m.CreatedAt = m.ChangedAt
	}
}

func (m *MemoryModel) To() *types.Memory {
	memory := &types.Memory{
		ID:       m.ID,
		Metadata: make(map[string]string),
		Overview: m.Overview,
		Details:  m.Details,
		Relevant: m.Relevant,
		Comment:  m.Comment,
	}

	jsonData(string(m.Metadata), &memory.Metadata)
	memory.Time = time.Unix(0, m.CreatedAt)
	return nil
}

type DocumentModel struct {
	ID          string `gorm:"column:id;primaryKey"`
	Metadata    JSON   `gorm:"column:metadata"`
	Title       string `gorm:"column:title"`
	MIMEType    string `gorm:"column:mimetype"`
	Content     string `gorm:"column:content"`
	ContentHash string `gorm:"column:content_hash"`
	CreatedAt   int64  `gorm:"column:created_at"`
	ChangedAt   int64  `gorm:"column:changed_at"`
}

func (d *DocumentModel) TableName() string {
	return "friday_documents"
}

func (d *DocumentModel) From(doc *types.Document) {
	d.ID = doc.ID
	d.Metadata, _ = json.Marshal(doc.Metadata)
	d.Title = doc.Title
	d.MIMEType = doc.MIMEType
	d.Content = doc.Content
	d.ChangedAt = time.Now().UnixNano()
	if d.CreatedAt == 0 {
		d.CreatedAt = d.ChangedAt
	}
}

func (d *DocumentModel) To() *types.Document {
	document := &types.Document{
		ID:       d.ID,
		Metadata: make(map[string]string),
		Title:    d.Title,
		MIMEType: d.MIMEType,
		Content:  d.Content,
	}
	jsonData(string(d.Metadata), &document.Metadata)
	return nil
}

type ChunkModel struct {
	ID        string `gorm:"column:id;primaryKey"`
	Type      string `gorm:"column:type;index:ck_type"`
	Metadata  JSON   `gorm:"column:metadata"`
	Content   string `gorm:"column:content"`
	Vector    string `gorm:"column:vector;type:vector"`
	CreatedAt int64  `gorm:"column:created_at"`
	ChangedAt int64  `gorm:"column:changed_at"`
}

func (v *ChunkModel) TableName() string {
	return "friday_chunks"
}

func (v *ChunkModel) From(ck *types.Chunk) {
	v.ID = ck.ID
	v.Type = ck.Type
	v.Metadata, _ = json.Marshal(ck.Metadata)
	v.Content = ck.Content
	v.Vector = jsonString(ck.Vector)
	v.ChangedAt = time.Now().UnixNano()
	if v.CreatedAt == 0 {
		v.CreatedAt = v.ChangedAt
	}
}

func (v *ChunkModel) To() *types.Chunk {
	ck := &types.Chunk{
		ID:       v.ID,
		Type:     v.Type,
		Metadata: make(map[string]string),
		Content:  v.Content,
		Vector:   make([]float64, 0),
	}

	jsonData(string(v.Metadata), &ck.Metadata)
	jsonData(v.Vector, &ck.Vector)
	return ck
}

// JSON
// https://gorm.io/docs/data_types.html
type JSON json.RawMessage

func (j *JSON) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", value))
	}

	result := json.RawMessage{}
	err := json.Unmarshal(bytes, &result)
	*j = JSON(result)
	return err
}

func (j *JSON) Value() (driver.Value, error) {
	if j == nil || len(*j) == 0 {
		return nil, nil
	}
	return json.RawMessage(*j).MarshalJSON()
}

func (j *JSON) GormDBDataType(db *gorm.DB, field *schema.Field) string {
	switch db.Dialector.Name() {
	case "mysql", "sqlite":
		return "JSON"
	case "postgres":
		return "JSONB"
	}
	return ""
}
