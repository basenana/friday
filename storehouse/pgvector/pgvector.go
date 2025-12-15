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
	"errors"
	"fmt"
	"time"

	"github.com/basenana/friday/providers"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/utils"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type DB struct {
	dEntity   *gorm.DB
	embedding providers.Embedding
	log       *zap.SugaredLogger
}

var _ storehouse.Sotrehouse = &DB{}
var _ storehouse.Vector = &DB{}

func New(postgresUrl string, embedding providers.Embedding) (*DB, error) {
	dbObj, err := gorm.Open(postgres.Open(postgresUrl), &gorm.Config{Logger: NewDbLogger()})
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

	if err = Migrate(dbObj); err != nil {
		return nil, err
	}

	return &DB{
		dEntity:   dbObj,
		embedding: embedding,
		log:       logger.New("pgvector"),
	}, nil
}

func (p *DB) Save(ctx context.Context, chunks ...*storehouse.Chunk) ([]*storehouse.Chunk, error) {
	var err error
	for _, chunk := range chunks {
		defaultChunkSetup(chunk)
		if len(chunk.Vector) == 0 {
			chunk.Vector, err = p.embedding.Vectorization(ctx, chunk.Content)
			if err != nil {
				return nil, err
			}
		}
	}

	err = p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {

		for _, chunk := range chunks {
			model := &ChunkModel{}

			if chunk.ID != "" {
				// to update
				res := tx.Where("id = ?", chunk.ID).Find(model)
				if res.Error != nil {
					return res.Error
				}

				model.From(chunk)
				res = tx.Save(model)
				if res.Error != nil {
					return res.Error
				}
			} else {
				// to create
				chunk.ID = newChunkID()
				model.From(chunk)
				res := tx.Create(model)
				if res.Error != nil {
					return res.Error
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return chunks, nil
}

func (p *DB) Get(ctx context.Context, id string) (*storehouse.Chunk, error) {
	vModel := &ChunkModel{}
	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		res = tx.Where("id = ?", id).First(vModel)
		if res.Error != nil {
			return res.Error
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return vModel.To(), nil
}

func (p *DB) Delete(ctx context.Context, id string) error {
	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		res = tx.Where("id = ?", id).Delete(&ChunkModel{})
		if res.Error != nil && !errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return res.Error
		}
		return nil
	})

	if err != nil {
		return err
	}
	return nil
}

func (p *DB) Filter(ctx context.Context, chunkType string, metadata map[string]string) ([]*storehouse.Chunk, error) {
	var (
		chunks []*storehouse.Chunk
		models []*ChunkModel
		tx     = p.dEntity.WithContext(ctx)
	)
	if chunkType != storehouse.TypeAll {
		tx = tx.Where("type = ?", chunkType)
	}
	for key, value := range metadata {
		tx = tx.Where(fmt.Sprintf("friday_chunks.metadata ->> '%s' = ?", key), value)
	}
	err := tx.Find(&models).Error
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		chunks = append(chunks, model.To())
	}
	return chunks, nil
}

func (p *DB) QueryVector(ctx context.Context, chunkType string, vector []float64, k int) ([]*storehouse.Chunk, error) {
	var (
		vectorModels = make([]ChunkModel, 0)
		result       = make([]*storehouse.Chunk, 0)
	)
	if err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		res = p.dEntity.WithContext(ctx)
		if chunkType != storehouse.TypeAll {
			res = res.Where("type = ?", chunkType)
		}
		res = res.Order(fmt.Sprintf("vector <-> '%s' DESC", jsonString(vector))).Limit(k).Find(&vectorModels)
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

func (p *DB) SemanticQuery(ctx context.Context, chunkType, query string, k int) ([]*storehouse.Chunk, error) {
	p.log.Infow("semantic query", "chunkType", chunkType, "query", query)
	vector, err := p.embedding.Vectorization(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed to vector: %w", err)
	}
	return p.QueryVector(ctx, chunkType, vector, k)
}

func (p *DB) SearchTools() []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("semantic_query",
			tools.WithDescription("Semantic search capabilities, using natural language to query and recall content from knowledge bases, which helps obtain more accurate relevant information."),
			tools.WithString("query",
				tools.Required(),
				tools.Description("Describe your problem using natural language. For search accuracy, query only one simple question at a time."),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				query, ok := request.Arguments["query"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: query")
				}

				chunks, err := p.SemanticQuery(ctx, storehouse.TypeAll, query, 3)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				if len(chunks) == 0 {
					return tools.NewToolResultError("no results found"), nil
				}

				return tools.NewToolResultText(utils.Res2Str(chunks)), nil
			}),
		),
	}
}
