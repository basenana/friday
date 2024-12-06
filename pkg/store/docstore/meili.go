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

package docstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"go.uber.org/zap"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/utils/logger"
)

type MeiliClient struct {
	log          *zap.SugaredLogger
	meiliUrl     string
	masterKey    string
	adminApiKey  string
	searchApiKey string
	index        meilisearch.IndexManager
	client       meilisearch.ServiceManager
}

var _ DocStoreInterface = &MeiliClient{}

func NewMeiliClient(conf config.Config) (DocStoreInterface, error) {
	client := meilisearch.New(conf.MeiliConfig.MeiliUrl, meilisearch.WithAPIKey(conf.MeiliConfig.MasterKey))
	index := client.Index(conf.MeiliConfig.Index)

	log := logger.NewLog("meilisearch")
	meiliClient := &MeiliClient{
		log:          log,
		meiliUrl:     conf.MeiliConfig.MeiliUrl,
		masterKey:    conf.MeiliConfig.MasterKey,
		adminApiKey:  conf.MeiliConfig.AdminApiKey,
		searchApiKey: conf.MeiliConfig.SearchApiKey,
		index:        index,
		client:       client,
	}
	filterableAttrs := append(doc.DocFilterableAttrs, doc.DocAttrFilterableAttrs...)
	t, err := client.Index(conf.MeiliConfig.Index).UpdateFilterableAttributes(&filterableAttrs)
	if err != nil {
		return nil, err
	}
	if err = meiliClient.wait(context.TODO(), t.TaskUID); err != nil {
		return nil, err
	}
	sortAttrs := doc.DocSortAttrs
	t, err = client.Index(conf.MeiliConfig.Index).UpdateSortableAttributes(&sortAttrs)
	if err != nil {
		return nil, err
	}
	return meiliClient, meiliClient.wait(context.TODO(), t.TaskUID)
}

func (c *MeiliClient) Store(ctx context.Context, docPtr doc.DocPtrInterface) error {
	task, err := c.index.AddDocuments(docPtr, "id")
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, task.TaskUID); err != nil {
		c.log.Errorf("store document with entryId %s error: %s", docPtr.EntryID(), err)
		return err
	}
	return nil
}

func (c *MeiliClient) FilterAttr(ctx context.Context, query *doc.DocumentAttrQuery) ([]doc.DocumentAttr, error) {
	rep, err := c.index.Search("", query.ToRequest())
	if err != nil {
		return nil, err
	}
	var attrs []doc.DocumentAttr
	for _, hit := range rep.Hits {
		b, _ := json.Marshal(hit)
		var attr doc.DocumentAttr
		err = json.Unmarshal(b, &attr)
		if err != nil {
			c.log.Errorf("unmarshal document attr error: %s", err)
			continue
		}
		attrs = append(attrs, attr)
	}
	return attrs, nil
}

func (c *MeiliClient) Search(ctx context.Context, query *doc.DocumentQuery) ([]doc.Document, error) {
	rep, err := c.index.Search(query.Search, query.ToRequest())
	if err != nil {
		return nil, err
	}
	var documents []doc.Document
	for _, hit := range rep.Hits {
		b, _ := json.Marshal(hit)
		var document doc.Document
		err = json.Unmarshal(b, &document)
		if err != nil {
			c.log.Errorf("unmarshal document error: %s", err)
			continue
		}
		documents = append(documents, document)
	}
	return documents, nil
}

func (c *MeiliClient) Update(ctx context.Context, document *doc.Document) error {
	t, err := c.index.UpdateDocuments(document)
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, t.TaskUID); err != nil {
		c.log.Errorf("update document %s error: %s", document.ID, err)
		return err
	}
	return nil
}

func (c *MeiliClient) Delete(ctx context.Context, docId string) error {
	t, err := c.index.DeleteDocument(docId)
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, t.TaskUID); err != nil {
		c.log.Errorf("delete document %s error: %s", docId, err)
		return err
	}
	return nil
}

func (c *MeiliClient) DeleteByFilter(ctx context.Context, aqs []doc.AttrQuery) error {
	filter := []interface{}{}
	for _, aq := range aqs {
		filter = append(filter, aq.ToFilter())
	}

	t, err := c.index.DeleteDocumentsByFilter(filter)
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, t.TaskUID); err != nil {
		c.log.Errorf("delete document by filter error: %s", err)
		return err
	}
	return nil
}

func (c *MeiliClient) wait(ctx context.Context, taskUID int64) error {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context timeout")
		case <-t.C:
			t, err := c.index.GetTask(taskUID)
			if err != nil {
				c.log.Error(err)
				return err
			}
			if t.Status == meilisearch.TaskStatusFailed {
				err := fmt.Errorf("task %d failed: %s", taskUID, t.Error)
				return err
			}
			if t.Status == meilisearch.TaskStatusCanceled {
				err := fmt.Errorf("task %d canceled: %s", taskUID, t.Error)
				return err
			}
			if t.Status == meilisearch.TaskStatusSucceeded {
				return nil
			}
		}
	}
}
