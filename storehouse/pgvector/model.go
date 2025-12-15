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

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/basenana/friday/storehouse"
)

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

func (v *ChunkModel) From(ck *storehouse.Chunk) {
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

func (v *ChunkModel) To() *storehouse.Chunk {
	ck := &storehouse.Chunk{
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
