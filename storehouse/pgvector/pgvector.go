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
	"strings"
	"time"

	"github.com/basenana/friday/providers"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/storehouse/chunks"
	"github.com/basenana/friday/types"
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

var _ storehouse.Storehouse = &DB{}
var _ storehouse.Vector = &DB{}
var _ storehouse.SearchEngine = &DB{}

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

func (p *DB) ListSessions(ctx context.Context, filter map[string]string, includesClosed bool) ([]*types.Session, error) {
	var (
		sessions []*types.Session
		models   []*SessionModel
		tx       = p.dEntity.WithContext(ctx)
	)

	if filter == nil {
		filter = map[string]string{}
	}
	if !includesClosed {
		filter[types.MetadataSessionState] = types.MetadataSessionStateOpen
	}

	for key, value := range filter {
		tx = tx.Where(fmt.Sprintf("%s.metadata ->> '%s' = ?", (&SessionModel{}).TableName(), key), value)
	}
	err := tx.Find(&models).Error
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		sessions = append(sessions, model.To())
	}
	return sessions, nil
}

func (p *DB) CreateSession(ctx context.Context, session *types.Session) (*types.Session, error) {
	if session.ID == "" {
		session.ID = newRecordID()
	}

	model := &SessionModel{}
	model.From(session)
	if err := p.dEntity.WithContext(ctx).Save(model).Error; err != nil {
		return nil, err
	}
	return session, nil
}

func (p *DB) UpdateSession(ctx context.Context, session *types.Session) error {
	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := &SessionModel{}
		res := tx.WithContext(ctx).Where("id = ?", session.ID).Find(model)
		if res.Error != nil {
			return res.Error
		}

		model.From(session)
		return tx.WithContext(ctx).Save(model).Error
	})
	if err != nil {
		return err
	}
	return nil
}

func (p *DB) OpenSession(ctx context.Context, sessionID string) (*types.Session, error) {
	var session *types.Session
	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := &SessionModel{}
		res := tx.WithContext(ctx).Where("id = ?", sessionID).Find(model)
		if res.Error != nil {
			return res.Error
		}

		model.LastOpenedAt = time.Now().UnixNano()
		res = tx.Save(model)
		if res.Error != nil {
			return res.Error
		}

		session = model.To()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (p *DB) AppendMessages(ctx context.Context, sessionID string, messages ...*types.Message) error {
	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, message := range messages {
			model := &MessageModel{}
			model.From(sessionID, message)
			if err := tx.Create(model).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *DB) ListMessages(ctx context.Context, sessionID string) ([]*types.Message, error) {
	var (
		messages []*types.Message
		models   []*MessageModel
		tx       = p.dEntity.WithContext(ctx)
	)
	tx = tx.Where("session_id = ?", sessionID).Order("created_at ASC")
	err := tx.Find(&models).Error
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		messages = append(messages, model.To())
	}
	return messages, nil
}

func (p *DB) CloseSession(ctx context.Context, sessionID string) error {
	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := &SessionModel{}
		if err := tx.Where("id = ?", sessionID).First(model).Error; err != nil {
			return err
		}

		metadata := make(map[string]string)
		_ = json.Unmarshal(model.Metadata, &metadata)
		if len(metadata) == 0 || metadata[types.MetadataSessionState] != types.MetadataSessionStateClosed {
			metadata[types.MetadataSessionState] = types.MetadataSessionStateClosed
		}
		model.Metadata, _ = json.Marshal(metadata)
		return tx.Save(model).Error
	})
}

func (p *DB) GetMemory(ctx context.Context, memoryID string) (*types.Memory, error) {
	var (
		model = &MemoryModel{}
		tx    = p.dEntity.WithContext(ctx)
	)
	tx = tx.Where("id = ?", memoryID)
	err := tx.First(model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, types.ErrNotFound
		}
		return nil, err
	}

	now := time.Now()
	model.UsageCount += 1
	model.LastUsedAt = now.UnixNano()
	if err := p.dEntity.WithContext(ctx).Save(model).Error; err != nil {
		return nil, err
	}

	return model.To(), nil
}

func (p *DB) AppendMemories(ctx context.Context, memories ...*types.Memory) error {
	var (
		chunkList []*types.Chunk
		err       error
	)

	for _, memory := range memories {
		if memory.ID == "" {
			memory.ID = newRecordID()
		}
		if memory.Metadata == nil {
			memory.Metadata = make(map[string]string)
		}
		if memory.CreatedAt.IsZero() {
			memory.CreatedAt = time.Now()
		}
		memory.Metadata[types.MetadataMemory] = memory.ID

		chunk := chunks.MemoryToChunkCard(memory)
		if err = defaultChunkSetup(ctx, chunk, p.embedding); err != nil {
			return err
		}
		chunkList = append(chunkList, chunk)
	}

	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, mem := range memories {
			model := &MemoryModel{}
			model.From(mem)
			if err := tx.Create(model).Error; err != nil {
				return err
			}
		}
		// Prepare ChunkModels outside transaction
		chunkModels := make([]*ChunkModel, 0, len(chunkList))
		for _, ck := range chunkList {
			m := &ChunkModel{}
			m.From(ck)
			chunkModels = append(chunkModels, m)
		}
		return p.saveChunksInTransaction(tx, chunkModels...)
	})
}

func (p *DB) FilterMemories(ctx context.Context, filter map[string]string) ([]*types.Memory, error) {
	var (
		memories []*types.Memory
		models   []*MemoryModel
		tx       = p.dEntity.WithContext(ctx)
	)

	for key, value := range filter {
		switch key {
		case types.MetadataMemoryType:
			tx = tx.Where("type = ?", value)
		case types.MetadataMemoryCategory:
			tx = tx.Where("category = ?", value)
		default:
			tx = tx.Where(fmt.Sprintf("%s.metadata ->> '%s' = ?", (&MemoryModel{}).TableName(), key), value)
		}
	}
	err := tx.Find(&models).Error
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		memories = append(memories, model.To())
	}
	return memories, nil
}

func (p *DB) ForgetMemory(ctx context.Context, memoryID string) error {
	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		res = tx.Where("id = ?", memoryID).Delete(&MemoryModel{})
		if res.Error != nil && !errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return res.Error
		}
		return p.deleteChunksInTransaction(tx, types.TypeMemory, map[string]string{types.MetadataMemory: memoryID})
	})

	if err != nil {
		return err
	}
	return nil
}

func (p *DB) ForgetMemories(ctx context.Context, shouldForget func(*types.Memory) bool, lastUsedDaysAgo int) error {
	threshold := time.Now().AddDate(0, 0, -lastUsedDaysAgo)
	var models []*MemoryModel
	if err := p.dEntity.WithContext(ctx).
		Where("last_used_at < ?", threshold.UnixNano()).
		Find(&models).Error; err != nil {
		return err
	}

	var resultErr error
	for _, m := range models {
		memory := m.To()
		if shouldForget(memory) {
			p.log.Infow("need to forget memory", "id", memory.ID)
			if err := p.ForgetMemory(ctx, memory.ID); err != nil {
				p.log.Errorw("forget memory failed", "id", memory.ID, "err", err)
				resultErr = err
			}
		}
	}
	return resultErr
}

func (p *DB) GetDocument(ctx context.Context, docID string) (*types.Document, error) {
	var (
		model = &DocumentModel{}
		tx    = p.dEntity.WithContext(ctx)
	)
	tx = tx.Where("id = ?", docID)
	err := tx.First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, types.ErrNotFound
		}
		return nil, err
	}
	return model.To(), nil
}

func (p *DB) CreateDocument(ctx context.Context, document *types.Document) error {
	if document.ID == "" {
		// The document ID must be specified at the outside.
		return fmt.Errorf("document id is empty")
	}

	var (
		contentHash = utils.ComputeStructHash(document.Content, nil)
		metadata    = make(map[string]string)
		err         error
	)

	for k, v := range document.Metadata {
		metadata[k] = v
	}
	metadata[types.MetadataDocument] = document.ID
	metadata[types.MetadataChunkDocument] = contentHash

	var chunkList []*types.Chunk
	switch strings.ToLower(document.MIMEType) {
	case "text/plain", "text/markdown":
		chunkList = chunks.SplitTextContent(types.TypeDocument, metadata, document.Content, chunks.SplitConfig{})
	case "text/html":
		chunkList = chunks.SplitHTMLContent(types.TypeDocument, metadata, document.Content, chunks.SplitConfig{})
	default:
		chunkList = chunks.SplitTextContent(types.TypeDocument, metadata, document.Content, chunks.SplitConfig{})
	}

	for _, chunk := range chunkList {
		if err = defaultChunkSetup(ctx, chunk, p.embedding); err != nil {
			return err
		}
	}

	model := &DocumentModel{}
	model.From(document)
	model.ContentHash = contentHash

	// Prepare ChunkModels outside transaction
	chunkModels := make([]*ChunkModel, 0, len(chunkList))
	for _, ck := range chunkList {
		m := &ChunkModel{}
		m.From(ck)
		chunkModels = append(chunkModels, m)
	}

	tokenExpr := toTsVectorExpr(document)
	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err = tx.Create(model).Error; err != nil {
			return err
		}
		res := tx.Model(&DocumentModel{}).Where("id = ?", model.ID).Update("token", gorm.Expr(tokenExpr))
		if res.Error != nil {
			return res.Error
		}
		return p.saveChunksInTransaction(tx, chunkModels...)
	})
}

func (p *DB) UpdateDocument(ctx context.Context, document *types.Document) error {
	if document.ID == "" {
		return fmt.Errorf("document id is empty")
	}

	var (
		model       = &DocumentModel{}
		contentHash = utils.ComputeStructHash(document.Content, nil)
		metadata    = make(map[string]string)
		err         error
	)

	err = p.dEntity.WithContext(ctx).Where("id = ?", document.ID).First(model).Error
	if err != nil {
		return err
	}

	if model.ContentHash == contentHash {
		return nil
	}

	for k, v := range document.Metadata {
		metadata[k] = v
	}
	metadata[types.MetadataDocument] = document.ID
	metadata[types.MetadataChunkDocument] = contentHash

	chunkList := chunks.SplitTextContent(types.TypeDocument, metadata, document.Content, chunks.SplitConfig{})
	for _, chunk := range chunkList {
		if err = defaultChunkSetup(ctx, chunk, p.embedding); err != nil {
			return err
		}
	}

	// Prepare ChunkModels outside transaction
	chunkModels := make([]*ChunkModel, 0, len(chunkList))
	for _, ck := range chunkList {
		m := &ChunkModel{}
		m.From(ck)
		chunkModels = append(chunkModels, m)
	}

	tokenExpr := toTsVectorExpr(document)
	return p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model.From(document)
		oldHash := model.ContentHash
		model.ContentHash = contentHash
		if err = p.dEntity.WithContext(ctx).Save(model).Error; err != nil {
			return err
		}

		res := tx.Model(&DocumentModel{}).Where("id = ?", model.ID).Update("token", gorm.Expr(tokenExpr))
		if res.Error != nil {
			return res.Error
		}

		if err = p.deleteChunksInTransaction(tx, types.TypeDocument, map[string]string{
			types.MetadataDocument:      document.ID,
			types.MetadataChunkDocument: oldHash,
		}); err != nil {
			return err
		}

		return p.saveChunksInTransaction(tx, chunkModels...)
	})
}

func (p *DB) DeleteDocument(ctx context.Context, docID string) error {
	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		res = tx.Where("id = ?", docID).Delete(&DocumentModel{})
		if res.Error != nil && !errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return res.Error
		}
		return p.deleteChunksInTransaction(tx, types.TypeDocument, map[string]string{
			types.MetadataDocument: docID,
		})
	})

	if err != nil {
		return err
	}
	return nil
}

func (p *DB) SaveChunks(ctx context.Context, chunkList ...*types.Chunk) ([]*types.Chunk, error) {
	var err error
	for _, chunk := range chunkList {
		err = defaultChunkSetup(ctx, chunk, p.embedding)
		if err != nil {
			return nil, err
		}
	}

	// Prepare ChunkModels outside transaction
	models := make([]*ChunkModel, 0, len(chunkList))
	for _, chunk := range chunkList {
		model := &ChunkModel{}
		model.From(chunk)
		models = append(models, model)
	}

	err = p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return p.saveChunksInTransaction(tx, models...)
	})

	if err != nil {
		return nil, err
	}

	return chunkList, nil
}

func (p *DB) saveChunksInTransaction(tx *gorm.DB, models ...*ChunkModel) error {
	for _, model := range models {
		var res *gorm.DB
		if model.ID != "" {
			// to update
			existing := &ChunkModel{}
			res = tx.Where("id = ?", model.ID).Find(existing)
			if res.Error != nil {
				return res.Error
			}
			existing.From(&types.Chunk{
				ID:       model.ID,
				Type:     model.Type,
				Metadata: map[string]string{},
				Content:  model.Content,
			})
			res = tx.Save(existing)
		} else {
			// to create
			model.ID = newRecordID()
			res = tx.Create(model)
		}
		if res.Error != nil {
			return res.Error
		}
	}
	return nil
}

func (p *DB) deleteChunksInTransaction(tx *gorm.DB, chunkType string, filter map[string]string) error {
	if chunkType == "" || filter == nil {
		return fmt.Errorf("invalid chunk delete filter")
	}

	tx = tx.Where("type = ?", chunkType)
	for key, value := range filter {
		tx = tx.Where(fmt.Sprintf("%s.metadata ->> '%s' = ?", (&ChunkModel{}).TableName(), key), value)
	}
	return tx.Delete(&ChunkModel{}).Error
}

func (p *DB) GetChunk(ctx context.Context, id string) (*types.Chunk, error) {
	vModel := &ChunkModel{}
	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		res = tx.Where("id = ?", id).First(vModel)
		if res.Error != nil {
			if errors.Is(res.Error, gorm.ErrRecordNotFound) {
				return types.ErrNotFound
			}
			return res.Error
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return vModel.To(), nil
}

func (p *DB) DeleteChunk(ctx context.Context, id string) error {
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

func (p *DB) FilterChunks(ctx context.Context, chunkType string, filter map[string]string) ([]*types.Chunk, error) {
	var (
		chunkList []*types.Chunk
		models    []*ChunkModel
		tx        = p.dEntity.WithContext(ctx)
	)
	if chunkType != types.TypeAll {
		tx = tx.Where("type = ?", chunkType)
	}
	for key, value := range filter {
		tx = tx.Where(fmt.Sprintf("%s.metadata ->> '%s' = ?", (&ChunkModel{}).TableName(), key), value)
	}
	err := tx.Find(&models).Error
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		chunkList = append(chunkList, model.To())
	}
	return chunkList, nil
}

func (p *DB) QueryVector(ctx context.Context, chunkType string, filter map[string]string, vector []float64, k int) ([]*types.Chunk, error) {
	var (
		vectorModels = make([]ChunkModel, 0)
		result       = make([]*types.Chunk, 0)
	)
	if err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		res = p.dEntity.WithContext(ctx)
		if chunkType != types.TypeAll {
			res = res.Where("type = ?", chunkType)
		}
		for key, value := range filter {
			res = res.Where(fmt.Sprintf("%s.metadata ->> '%s' = ?", (&ChunkModel{}).TableName(), key), value)
		}
		res = res.Order(fmt.Sprintf("vector <-> '%s'", jsonString(vector))).Limit(k).Find(&vectorModels)
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

func (p *DB) SemanticQuery(ctx context.Context, chunkType string, filter map[string]string, query string, k int) ([]*types.Chunk, error) {
	p.log.Infow("semantic query", "chunkType", chunkType, "query", query)
	vector, err := p.embedding.Vectorization(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed to vector: %w", err)
	}
	return p.QueryVector(ctx, chunkType, filter, vector, k)
}

func (p *DB) QueryLanguage(ctx context.Context, query string) ([]*types.Document, error) {
	p.log.Infow("keyword query", "query", query)

	keywords := strings.Fields(query)
	if len(keywords) == 0 {
		return []*types.Document{}, nil
	}
	tsQuery := strings.Join(keywords, " & ")

	var (
		docModels []DocumentModel
		result    []*types.Document
	)

	err := p.dEntity.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&DocumentModel{}).
			Where("token @@ to_tsquery('simple', ?)", tsQuery).
			Select("*, ts_rank(token, to_tsquery('simple', ?)) as rank", tsQuery)

		res = res.Order("rank DESC").Find(&docModels)
		return res.Error
	})

	if err != nil {
		return nil, err
	}

	for _, dm := range docModels {
		result = append(result, dm.To())
	}
	return result, nil
}
