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

package vectorstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/utils/logger"
	"github.com/basenana/friday/pkg/vectorstore/db"
)

type PostgresClient struct {
	log     logger.Logger
	dEntity *db.Entity
}

var _ VectorStore = &PostgresClient{}

func NewPostgresClient(postgresUrl string) (*PostgresClient, error) {
	dbObj, err := gorm.Open(postgres.Open(postgresUrl), &gorm.Config{Logger: gormlogger.Default})
	if err != nil {
		panic(err)
	}

	dbConn, err := dbObj.DB()
	if err != nil {
		return nil, err
	}

	dbConn.SetMaxIdleConns(5)
	dbConn.SetMaxOpenConns(50)
	dbConn.SetConnMaxLifetime(time.Hour)

	if err = dbConn.Ping(); err != nil {
		return nil, err
	}

	dbEnt, err := db.NewDbEntity(dbObj)
	if err != nil {
		return nil, err
	}

	return &PostgresClient{
		log:     logger.NewLogger("postgres"),
		dEntity: dbEnt,
	}, nil
}

func (p *PostgresClient) Store(id, content string, metadata map[string]interface{}, vectors []float32) error {
	ctx := context.Background()
	var m string
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		m = string(b)
	}
	v := &db.Vector{
		ID:        id,
		Source:    metadata["source"].(string),
		ParentDir: metadata["parentDir"].(string),
		Context:   content,
		Metadata:  m,
		Vector:    fmt.Sprintf("%v", vectors),
		CreatedAt: time.Now().UnixNano(),
		ChangedAt: time.Now().UnixNano(),
	}
	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		vModel := db.Vector{ID: id}
		res := tx.First(vModel)
		if res.Error != nil && res.Error != gorm.ErrRecordNotFound {
			return res.Error
		}

		if res.Error == gorm.ErrRecordNotFound {
			res = tx.Create(v)
			if res.Error != nil {
				return res.Error
			}
			return nil
		}

		vModel.Update(v)
		res = tx.Where("id = ?", id).Updates(vModel)
		if res.Error != nil || res.RowsAffected == 0 {
			if res.RowsAffected == 0 {
				return errors.New("operation conflict")
			}
			return res.Error
		}
		return nil
	})
}

func (p PostgresClient) Search(vectors []float32, k int) ([]models.Doc, error) {
	//TODO implement me
	panic("implement me")
}

func (p PostgresClient) Exist(id string) bool {
	//TODO implement me
	panic("implement me")
}
