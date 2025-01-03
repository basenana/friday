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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/basenana/friday/pkg/models/vector"
	"github.com/basenana/friday/pkg/store"
	"github.com/basenana/friday/pkg/store/db"
	"github.com/basenana/friday/pkg/store/utils"
	"github.com/basenana/friday/pkg/utils/logger"
)

type PgVectorClient struct {
	log     logger.Logger
	dEntity *db.Entity
}

var _ store.VectorStore = &PgVectorClient{}

func NewPgVectorClient(log logger.Logger, postgresUrl string) (*PgVectorClient, error) {
	if log == nil {
		log = logger.NewLogger("database")
	}
	dbObj, err := gorm.Open(postgres.Open(postgresUrl), &gorm.Config{Logger: utils.NewDbLogger()})
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

	return &PgVectorClient{
		log:     log,
		dEntity: dbEnt,
	}, nil
}

func (p *PgVectorClient) Store(ctx context.Context, element *vector.Element, extra map[string]any) error {
	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if extra == nil {
			extra = make(map[string]interface{})
		}
		extra["name"] = element.Name
		extra["group"] = element.Group

		var m string
		b, err := json.Marshal(extra)
		if err != nil {
			return err
		}
		m = string(b)
		vectorJson, _ := json.Marshal(element.Vector)

		var v *Index
		v = v.From(element)
		v.Extra = m
		v.Vector = string(vectorJson)

		vModel := &Index{}
		res := tx.Where("name = ? AND group = ?", element.Name, element.Group).First(vModel)
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
		res = tx.Where("name = ? AND group = ?", element.Name, element.Group).Updates(vModel)
		if res.Error != nil || res.RowsAffected == 0 {
			if res.RowsAffected == 0 {
				return errors.New("operation conflict")
			}
			return res.Error
		}
		return nil
	})
}

func (p *PgVectorClient) Search(ctx context.Context, query vector.VectorDocQuery, vectors []float32, k int) ([]*vector.Doc, error) {
	var (
		vectorModels = make([]Index, 0)
		result       = make([]*vector.Doc, 0)
	)
	if err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		vectorJson, _ := json.Marshal(vectors)
		var res *gorm.DB
		res = p.dEntity.DB.WithContext(ctx)
		if query.ParentId != 0 {
			res = res.Where("parent_entry_id = ?", query.ParentId)
		}
		if query.Oid != 0 {
			res = res.Where("oid = ?", query.ParentId)
		}
		res = res.Order(fmt.Sprintf("vector <-> '%s'", string(vectorJson))).Limit(k).Find(&vectorModels)
		if res.Error != nil {
			return res.Error
		}
		return nil
	}); err != nil {
		return nil, err
	}

	for _, v := range vectorModels {
		result = append(result, v.To())
	}
	return result, nil
}

func (p *PgVectorClient) Get(ctx context.Context, oid int64, name string, group int) (*vector.Element, error) {
	vModel := &Index{}
	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		if oid == 0 {
			res = tx.Where("name = ? AND group = ?", name, group).First(vModel)
		} else {
			res = tx.Where("name = ? AND oid = ? AND group = ?", name, oid, group).First(vModel)
		}
		if res.Error != nil && res.Error != gorm.ErrRecordNotFound {
			return res.Error
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return vModel.ToElement(), err
}
