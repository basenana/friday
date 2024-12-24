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

package memory

import (
	"context"
	"time"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/store"
	"github.com/basenana/friday/pkg/store/db"
	"github.com/basenana/friday/pkg/store/postgres"
	"github.com/basenana/friday/pkg/store/utils"
	"github.com/basenana/friday/pkg/utils/logger"
)

type MemoryClient struct {
	log     *zap.SugaredLogger
	dbStore postgres.PostgresClient
}

var _ store.DocStoreInterface = &MemoryClient{}

func NewMemoryMetaStore() (*MemoryClient, error) {
	log := logger.NewLog("memory")
	dbEntity, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: utils.NewDbLogger()})
	if err != nil {
		return nil, err
	}

	dbConn, err := dbEntity.DB()
	if err != nil {
		return nil, err
	}

	if err = dbConn.Ping(); err != nil {
		return nil, err
	}

	dbConn.SetMaxIdleConns(1)
	dbConn.SetMaxOpenConns(1)
	dbConn.SetConnMaxLifetime(time.Hour)

	dbEnt, err := db.NewDbEntity(dbEntity, db.Migrate)
	if err != nil {
		return nil, err
	}

	return &MemoryClient{
		dbStore: postgres.PostgresClient{
			Logger:  log,
			DEntity: dbEnt,
		},
	}, nil
}

func (m *MemoryClient) CreateDocument(ctx context.Context, doc *doc.Document) error {
	return m.dbStore.CreateDocument(ctx, doc)
}

func (m *MemoryClient) UpdateTokens(ctx context.Context, doc *doc.Document) error {
	return nil
}

func (m *MemoryClient) UpdateDocument(ctx context.Context, doc *doc.Document) error {
	return m.dbStore.UpdateDocument(ctx, doc)
}

func (m *MemoryClient) GetDocument(ctx context.Context, entryId int64) (*doc.Document, error) {
	return m.dbStore.GetDocument(ctx, entryId)
}

func (m *MemoryClient) FilterDocuments(ctx context.Context, filter *doc.DocumentFilter) ([]*doc.Document, error) {
	filter.Search = ""
	return m.dbStore.FilterDocuments(ctx, filter)
}

func (m *MemoryClient) DeleteDocument(ctx context.Context, docId int64) error {
	return m.dbStore.DeleteDocument(ctx, docId)
}
