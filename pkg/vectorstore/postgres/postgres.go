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
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cdipaolo/goml/base"
	"github.com/cdipaolo/goml/cluster"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/utils/logger"
	"github.com/basenana/friday/pkg/vectorstore"
	"github.com/basenana/friday/pkg/vectorstore/db"
)

type PostgresClient struct {
	log     logger.Logger
	dEntity *db.Entity
}

func NewPostgresClient(postgresUrl string) (*PostgresClient, error) {
	dbObj, err := gorm.Open(postgres.Open(postgresUrl), &gorm.Config{Logger: logger.NewDbLogger()})
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

	dbEnt, err := db.NewDbEntity(dbObj, Migrate)
	if err != nil {
		return nil, err
	}

	return &PostgresClient{
		log:     logger.NewLogger("postgres"),
		dEntity: dbEnt,
	}, nil
}

func (p *PostgresClient) Store(id, content string, metadata models.Metadata, extra map[string]interface{}, vectors []float32) error {
	ctx := context.Background()

	if extra == nil {
		extra = make(map[string]interface{})
	}
	extra["category"] = metadata.Category
	extra["group"] = metadata.Group

	var m string
	b, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	m = string(b)

	vectorJson, _ := json.Marshal(vectors)
	v := &Index{
		ID:        id,
		Name:      metadata.Source,
		ParentDir: metadata.ParentDir,
		Context:   content,
		Metadata:  m,
		Vector:    string(vectorJson),
		CreatedAt: time.Now().UnixNano(),
		ChangedAt: time.Now().UnixNano(),
	}
	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		vModel := Index{ID: id}
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

func (p *PostgresClient) Search(vectors []float32, k int) ([]models.Doc, error) {
	vectors64 := make([]float64, 0)
	for _, v := range vectors {
		vectors64 = append(vectors64, float64(v))
	}
	// query from db
	existVectors := [][]float64{}

	model := cluster.NewKNN(k, existVectors, vectors64, base.EuclideanDistance)

	// make predictions like usual
	_, err := model.Predict([]float64{-10, 1})
	if err != nil {
		return nil, err
	}
	// todo
	return nil, nil
}

func (p *PostgresClient) Exist(id string) (bool, error) {
	//TODO implement me
	panic("implement me")
}

var _ vectorstore.VectorStore = &PostgresClient{}

func (p *PostgresClient) Inited(ctx context.Context) (bool, error) {
	var count int64
	res := p.dEntity.WithContext(ctx).Model(&BleveKV{}).Count(&count)
	if res.Error != nil {
		return false, res.Error
	}

	return count > 0, nil
}
