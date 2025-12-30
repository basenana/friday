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
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

func buildMigrations() []*gormigrate.Migration {
	return []*gormigrate.Migration{
		{
			ID: "2023100700",
			Migrate: func(db *gorm.DB) error {
				return db.AutoMigrate(
					&SessionModel{},
					&MessageModel{},
					&DocumentModel{},
					&MemoryModel{},
					&ChunkModel{},
				)
			},
			Rollback: func(db *gorm.DB) error {
				return nil
			},
		},
		{
			ID: "2025122000",
			Migrate: func(db *gorm.DB) error {
				return db.AutoMigrate(
					&SessionModel{},
					&MessageModel{},
				)
			},
			Rollback: func(db *gorm.DB) error {
				return nil
			},
		},
		{
			ID: "2025123000",
			Migrate: func(db *gorm.DB) error {
				if err := db.AutoMigrate(&ChunkModel{}); err != nil {
					return err
				}
				return db.Exec(`
					UPDATE friday_chunks
					SET token = to_tsvector('simple', content)
					WHERE token IS NULL OR token = ''
				`).Error
			},
			Rollback: func(db *gorm.DB) error {
				return nil
			},
		},
	}
}

func Migrate(db *gorm.DB) error {
	options := gormigrate.DefaultOptions
	options.TableName = "friday_schema"
	m := gormigrate.New(db, options, buildMigrations())
	err := m.Migrate()
	return err
}
