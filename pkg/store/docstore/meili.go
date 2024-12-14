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
	"github.com/basenana/friday/pkg/utils"
	"github.com/basenana/friday/pkg/utils/logger"
)

type MeiliClient struct {
	log          *zap.SugaredLogger
	meiliUrl     string
	masterKey    string
	adminApiKey  string
	searchApiKey string
	docIndex     meilisearch.IndexManager
	attrIndex    meilisearch.IndexManager
	client       meilisearch.ServiceManager
}

var _ DocStoreInterface = &MeiliClient{}

func NewMeiliClient(conf config.Config) (DocStoreInterface, error) {
	client := meilisearch.New(conf.MeiliConfig.MeiliUrl, meilisearch.WithAPIKey(conf.MeiliConfig.MasterKey))
	docIndex := client.Index(conf.MeiliConfig.DocIndex)
	attrIndex := client.Index(conf.MeiliConfig.AttrIndex)

	log := logger.NewLog("meilisearch")
	meiliClient := &MeiliClient{
		log:          log,
		meiliUrl:     conf.MeiliConfig.MeiliUrl,
		masterKey:    conf.MeiliConfig.MasterKey,
		adminApiKey:  conf.MeiliConfig.AdminApiKey,
		searchApiKey: conf.MeiliConfig.SearchApiKey,
		docIndex:     docIndex,
		attrIndex:    attrIndex,
		client:       client,
	}
	return meiliClient, meiliClient.init()
}

func (c *MeiliClient) init() error {
	attrs, err := c.docIndex.GetFilterableAttributes()
	if err != nil {
		return err
	}
	if !utils.Equal(doc.DocFilterableAttrs, attrs) {
		t, err := c.docIndex.UpdateFilterableAttributes(&doc.DocFilterableAttrs)
		if err != nil {
			return err
		}
		if err = c.wait(context.TODO(), "document", t.TaskUID); err != nil {
			return err
		}
	}

	sortAttrs := doc.DocSortAttrs
	crtSortAttrs, err := c.docIndex.GetSortableAttributes()
	if err != nil {
		return err
	}
	if !utils.Equal(sortAttrs, crtSortAttrs) {
		t, err := c.docIndex.UpdateSortableAttributes(&sortAttrs)
		if err != nil {
			return err
		}
		if err = c.wait(context.TODO(), "document", t.TaskUID); err != nil {
			return err
		}
	}

	// attr index
	attrAttrs, err := c.attrIndex.GetFilterableAttributes()
	if err != nil {
		return err
	}
	if !utils.Equal(doc.DocAttrFilterableAttrs, attrAttrs) {
		t, err := c.docIndex.UpdateFilterableAttributes(&doc.DocAttrFilterableAttrs)
		if err != nil {
			return err
		}
		if err = c.wait(context.TODO(), "attr", t.TaskUID); err != nil {
			return err
		}
	}
	attrSortAttrs := doc.DocAttrSortAttrs
	crtAttrSortAttrs, err := c.docIndex.GetSortableAttributes()
	if err != nil {
		return err
	}
	if !utils.Equal(attrSortAttrs, crtAttrSortAttrs) {
		t, err := c.docIndex.UpdateSortableAttributes(&attrSortAttrs)
		if err != nil {
			return err
		}
		if err = c.wait(context.TODO(), "attr", t.TaskUID); err != nil {
			return err
		}
	}
	return nil
}

func (c *MeiliClient) index(kind string) meilisearch.IndexManager {
	if kind == "attr" {
		return c.attrIndex
	}
	return c.docIndex
}

func (c *MeiliClient) Store(ctx context.Context, docPtr doc.DocPtrInterface) error {
	c.log.Debugf("store entryId %s %s: %s", docPtr.EntryID(), docPtr.Type(), docPtr.String())
	task, err := c.index(docPtr.Type()).AddDocuments(docPtr, "id")
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, docPtr.Type(), task.TaskUID); err != nil {
		c.log.Errorf("store document with entryId %s error: %s", docPtr.EntryID(), err)
	}
	return nil
}

func (c *MeiliClient) FilterAttr(ctx context.Context, query *doc.DocumentAttrQuery) (doc.DocumentAttrList, error) {
	c.log.Debugf("query document attr : [%s]", query.String())
	rep, err := c.index("attr").Search("", query.ToRequest())
	if err != nil {
		return nil, err
	}
	attrs := doc.DocumentAttrList{}
	for _, hit := range rep.Hits {
		b, _ := json.Marshal(hit)
		attr := &doc.DocumentAttr{}
		err = json.Unmarshal(b, &attr)
		if err != nil {
			c.log.Errorf("unmarshal document attr error: %s", err)
			continue
		}
		attrs = append(attrs, attr)
	}
	return attrs, nil
}

func (c *MeiliClient) Search(ctx context.Context, query *doc.DocumentQuery) (doc.DocumentList, error) {
	c.log.Debugf("search document: [%s] query: [%s]", query.Search, query.String())
	rep, err := c.index("document").Search(query.Search, query.ToRequest())
	if err != nil {
		return nil, err
	}
	documents := doc.DocumentList{}
	for _, hit := range rep.Hits {
		b, _ := json.Marshal(hit)
		document := &doc.Document{}
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
	c.log.Debugf("update document: %s", document.ID())
	t, err := c.index(document.Type()).UpdateDocuments(document)
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, document.Type(), t.TaskUID); err != nil {
		c.log.Errorf("update document %s error: %s", document.ID, err)
	}
	return nil
}

func (c *MeiliClient) Delete(ctx context.Context, docId string) error {
	c.log.Debugf("delete document: %s", docId)
	t, err := c.index("document").DeleteDocument(docId)
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, "document", t.TaskUID); err != nil {
		c.log.Errorf("delete document %s error: %s", docId, err)
	}
	return nil
}

func (c *MeiliClient) DeleteByFilter(ctx context.Context, aqs doc.DocumentAttrQuery) error {
	c.log.Debugf("delete by filter: %+v", aqs.String())
	filter := []interface{}{}
	for _, aq := range aqs.AttrQueries {
		filter = append(filter, aq.ToFilter())
	}

	t, err := c.index("attr").DeleteDocumentsByFilter(filter)
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, "attr", t.TaskUID); err != nil {
		c.log.Errorf("delete document by filter error: %s", err)
	}
	return nil
}

func (c *MeiliClient) wait(ctx context.Context, kind string, taskUID int64) error {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context timeout")
		case <-t.C:
			t, err := c.index(kind).GetTask(taskUID)
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
