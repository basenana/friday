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
	"errors"
	"runtime/trace"

	"gorm.io/gorm"

	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/store"
	"github.com/basenana/friday/pkg/store/db"
	"github.com/basenana/friday/pkg/store/utils"
)

var _ store.DocStoreInterface = &PostgresClient{}

func (p *PostgresClient) CreateDocument(ctx context.Context, doc *doc.Document) error {
	defer trace.StartRegion(ctx, "store.doc.CreateDocument").End()
	err := p.DEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		docMod := &db.Document{}
		res := tx.Where("oid = ?", doc.EntryId).First(docMod)
		if res.Error != nil {
			if errors.Is(res.Error, gorm.ErrRecordNotFound) {
				docMod = docMod.From(doc)
				res = tx.Create(docMod)
				if res.Error != nil {
					return res.Error
				}

			}
			return res.Error
		}
		docMod = docMod.From(doc)
		res = tx.Save(docMod)
		return res.Error
	})
	if err != nil {
		return utils.SqlError2Error(err)
	}
	return nil
}

func (p *PostgresClient) UpdateTokens(ctx context.Context, doc *doc.Document) error {
	defer trace.StartRegion(ctx, "store.doc.UpdateTokens").End()
	err := p.DEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		docMod := &db.Document{}
		res := tx.Where("oid = ?", doc.EntryId).First(docMod)
		if res.Error != nil {
			return res.Error
		}
		docMod = docMod.Tokens(doc)
		p.Logger.Info("update token", docMod.Token)
		res = tx.Model(&db.Document{}).Where("id = ?", docMod.ID).Update("token", gorm.Expr(string(docMod.Token)))
		return res.Error
	})
	if err != nil {
		return utils.SqlError2Error(err)
	}
	return nil
}

func (p *PostgresClient) UpdateDocument(ctx context.Context, doc *doc.Document) error {
	defer trace.StartRegion(ctx, "store.doc.UpdateDocument").End()
	err := p.DEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		docMod := &db.Document{}
		res := tx.Where("oid = ?", doc.EntryId).First(docMod)
		if res.Error != nil {
			if errors.Is(res.Error, gorm.ErrRecordNotFound) {
				docMod = docMod.From(doc)
				res = tx.Create(docMod)
				return res.Error
			}
			return res.Error
		}
		docMod = docMod.UpdateFrom(doc)
		res = tx.Save(docMod)
		return res.Error
	})
	if err != nil {
		return utils.SqlError2Error(err)
	}
	return nil
}

func (p *PostgresClient) GetDocument(ctx context.Context, entryId int64) (*doc.Document, error) {
	defer trace.StartRegion(ctx, "store.doc.GetDocument").End()
	doc := &db.Document{}
	res := p.DEntity.WithNamespace(ctx).Where("oid = ?", entryId).First(doc)
	if res.Error != nil {
		return nil, utils.SqlError2Error(res.Error)
	}
	return doc.To(), nil
}

func (p *PostgresClient) FilterDocuments(ctx context.Context, filter *doc.DocumentFilter) ([]*doc.Document, error) {
	defer trace.StartRegion(ctx, "store.doc.FilterDocuments").End()
	docList := make([]db.Document, 0)
	q := p.WithNamespace(ctx)
	if page := models.GetPagination(ctx); page != nil {
		q = q.Offset(page.Offset()).Limit(page.Limit())
	}
	res := docOrder(p.docQueryFilter(q, filter), &filter.Order).Find(&docList)
	if res.Error != nil {
		return nil, utils.SqlError2Error(res.Error)
	}

	result := make([]*doc.Document, len(docList))
	for i, doc := range docList {
		result[i] = doc.To()
	}
	return result, nil
}

func (p *PostgresClient) DeleteDocument(ctx context.Context, entryId int64) error {
	defer trace.StartRegion(ctx, "store.doc.DeleteDocument").End()
	err := p.DEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := namespaceQuery(ctx, tx).Where("oid = ?", entryId).Delete(&db.Document{})
		return res.Error
	})
	return utils.SqlError2Error(err)
}

func (p *PostgresClient) docQueryFilter(tx *gorm.DB, filter *doc.DocumentFilter) *gorm.DB {
	if filter.ParentID != nil {
		tx = tx.Where("document.parent_entry_id = ?", filter.ParentID)
	}
	if filter.Marked != nil {
		tx = tx.Where("document.marked = ?", *filter.Marked)
	}
	if filter.Unread != nil {
		tx = tx.Where("document.unread = ?", *filter.Unread)
	}
	if filter.Source != "" {
		tx = tx.Where("document.source = ?", filter.Source)
	}
	if filter.CreatedAtStart != nil {
		tx = tx.Where("document.created_at >= ?", *filter.CreatedAtStart)
	}
	if filter.CreatedAtEnd != nil {
		tx = tx.Where("document.created_at < ?", *filter.CreatedAtEnd)
	}
	if filter.ChangedAtStart != nil {
		tx = tx.Where("document.changed_at >= ?", *filter.ChangedAtStart)
	}
	if filter.ChangedAtEnd != nil {
		tx = tx.Where("document.changed_at < ?", *filter.ChangedAtEnd)
	}
	if filter.FuzzyName != "" {
		tx = tx.Where("document.name LIKE ?", "%"+filter.FuzzyName+"%")
	}
	if filter.Search != "" {
		tx = tx.Where("document.token @@ to_tsquery('simple', ?)", filter.Search).
			Select("*, ts_rank(document.token, to_tsquery('simple', ?)) as rank", filter.Search).
			Order("rank DESC")
	}
	return tx
}

func docOrder(tx *gorm.DB, order *doc.DocumentOrder) *gorm.DB {
	if order != nil {
		orderStr := order.Order.String()
		if order.Desc {
			orderStr += " DESC"
		}
		tx = tx.Order(orderStr)
	} else {
		tx = tx.Order("created_at DESC")
	}
	return tx
}

func namespaceQuery(ctx context.Context, tx *gorm.DB) *gorm.DB {
	ns := models.GetNamespace(ctx)
	if ns.String() == models.DefaultNamespaceValue {
		return tx
	}
	return tx.Where("namespace = ?", ns.String())
}

func (p *PostgresClient) WithNamespace(ctx context.Context) *gorm.DB {
	ns := models.GetNamespace(ctx)
	if ns.String() == models.DefaultNamespaceValue {
		return p.DEntity.WithContext(ctx)
	}
	return p.DEntity.WithContext(ctx).Where("namespace = ?", ns.String())
}
