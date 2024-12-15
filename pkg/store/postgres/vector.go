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

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/cdipaolo/goml/base"
	"gorm.io/gorm"

	"github.com/basenana/friday/pkg/models/vector"
	"github.com/basenana/friday/pkg/store"
	"github.com/basenana/friday/pkg/utils"
)

func (p *PostgresClient) Store(ctx context.Context, element *vector.Element, extra map[string]any) error {
	namespace := ctx.Value("namespace")
	if namespace == nil {
		namespace = defaultNamespace
	}
	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if extra == nil {
			extra = make(map[string]interface{})
		}
		extra["name"] = element.Name
		extra["group"] = element.Group

		b, err := json.Marshal(extra)
		if err != nil {
			return err
		}

		var v *Index
		v, err = v.From(element)
		if err != nil {
			return err
		}

		v.Extra = string(b)
		v.Namespace = namespace.(string)

		vModel := &Index{}
		res := tx.Where("namespace = ? AND name = ? AND idx_group = ?", namespace, element.Name, element.Group).First(vModel)
		if res.Error != nil && res.Error != gorm.ErrRecordNotFound {
			return res.Error
		}

		if res.Error == gorm.ErrRecordNotFound {
			v.CreatedAt = time.Now().UnixNano()
			v.ChangedAt = time.Now().UnixNano()
			res = tx.Create(v)
			if res.Error != nil {
				return res.Error
			}
			return nil
		}

		vModel.Update(v)
		res = tx.Where("namespace = ? AND name = ? AND idx_group = ?", namespace, element.Name, element.Group).Updates(vModel)
		if res.Error != nil || res.RowsAffected == 0 {
			if res.RowsAffected == 0 {
				return errors.New("operation conflict")
			}
			return res.Error
		}
		return nil
	})
}

func (p *PostgresClient) Search(ctx context.Context, query vector.VectorDocQuery, vectors []float32, k int) ([]*vector.Doc, error) {
	vectors64 := make([]float64, 0)
	for _, v := range vectors {
		vectors64 = append(vectors64, float64(v))
	}
	// query from db
	existIndexes := make([]Index, 0)

	res := p.dEntity.WithNamespace(ctx)
	if query.ParentId != 0 {
		res = res.Where("parent_entry_id = ?", query.ParentId)
	}
	if query.Oid != 0 {
		res = res.Where("oid = ?", query.Oid)
	}
	res = res.Find(&existIndexes)
	if res.Error != nil {
		return nil, res.Error
	}

	// knn search
	dists := utils.Distances{}
	for _, index := range existIndexes {
		var vector []float64
		err := json.Unmarshal([]byte(index.Vector), &vector)
		if err != nil {
			return nil, err
		}

		dists = append(dists, utils.Distance{
			Object: index,
			Dist:   base.EuclideanDistance(vector, vectors64),
		})
	}

	sort.Sort(dists)

	minKIndexes := dists
	if k < len(dists) {
		minKIndexes = dists[0:k]
	}
	results := make([]*vector.Doc, 0)
	for _, index := range minKIndexes {
		i := index.Object.(Index)
		results = append(results, i.ToDoc())
	}

	return results, nil
}

func (p *PostgresClient) Get(ctx context.Context, oid int64, name string, group int) (*vector.Element, error) {
	vModel := &Index{}
	tx := p.dEntity.WithNamespace(ctx).Where("name = ? AND idx_group = ?", name, group)
	if oid != 0 {
		tx = tx.Where("oid = ?", oid)
	}
	res := tx.First(vModel)
	if res.Error != nil {
		if res.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, res.Error
	}
	return vModel.To()
}

var _ store.VectorStore = &PostgresClient{}

func (p *PostgresClient) Inited(ctx context.Context) (bool, error) {
	var count int64
	res := p.dEntity.WithContext(ctx).Model(&BleveKV{}).Count(&count)
	if res.Error != nil {
		return false, res.Error
	}

	return count > 0, nil
}
