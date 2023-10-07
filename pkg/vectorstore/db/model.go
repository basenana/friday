/*
 * Copyright 2023 Friday Author.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package db

import "time"

type Vector struct {
	ID        string `gorm:"column:id;primaryKey"`
	Source    string `gorm:"column:source;index:source"`
	ParentDir string `gorm:"column:parent_dir;index:parent_dir"`
	Context   string `gorm:"column:context"`
	Metadata  string `gorm:"column:metadata"`
	Vector    string `gorm:"column:vector"`
	CreatedAt int64  `gorm:"column:created_at"`
	ChangedAt int64  `gorm:"column:changed_at"`
}

func (v *Vector) TableName() string {
	return "vector"
}

func (v *Vector) Update(vector *Vector) {
	v.ID = vector.ID
	v.Source = vector.Source
	v.ParentDir = vector.ParentDir
	v.Context = vector.Context
	v.Metadata = vector.Metadata
	v.Vector = vector.Vector
	v.ChangedAt = time.Now().UnixNano()
}
