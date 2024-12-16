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

package meili

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"go.uber.org/zap"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/store"
	"github.com/basenana/friday/pkg/utils"
	"github.com/basenana/friday/pkg/utils/logger"
)

type Client struct {
	log          *zap.SugaredLogger
	meiliUrl     string
	masterKey    string
	adminApiKey  string
	searchApiKey string
	docIndex     meilisearch.IndexManager
	attrIndex    meilisearch.IndexManager
	client       meilisearch.ServiceManager
}

var _ store.DocStoreInterface = &Client{}

func NewMeiliClient(conf config.Config) (store.DocStoreInterface, error) {
	client := meilisearch.New(conf.DocStore.MeiliConfig.MeiliUrl, meilisearch.WithAPIKey(conf.DocStore.MeiliConfig.MasterKey))
	docIndex := client.Index(conf.DocStore.MeiliConfig.DocIndex)
	attrIndex := client.Index(conf.DocStore.MeiliConfig.AttrIndex)

	log := logger.NewLog("meilisearch")
	meiliClient := &Client{
		log:          log,
		meiliUrl:     conf.DocStore.MeiliConfig.MeiliUrl,
		masterKey:    conf.DocStore.MeiliConfig.MasterKey,
		adminApiKey:  conf.DocStore.MeiliConfig.AdminApiKey,
		searchApiKey: conf.DocStore.MeiliConfig.SearchApiKey,
		docIndex:     docIndex,
		attrIndex:    attrIndex,
		client:       client,
	}
	return meiliClient, meiliClient.init()
}

func (c *Client) init() error {
	attrs, err := c.docIndex.GetFilterableAttributes()
	if err != nil {
		return err
	}
	if !utils.Equal(DocFilterableAttrs, attrs) {
		t, err := c.docIndex.UpdateFilterableAttributes(&DocFilterableAttrs)
		if err != nil {
			return err
		}
		if err = c.wait(context.TODO(), "document", t.TaskUID); err != nil {
			return err
		}
	}

	sortAttrs := DocSortAttrs
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
	if !utils.Equal(DocAttrFilterableAttrs, attrAttrs) {
		t, err := c.docIndex.UpdateFilterableAttributes(&DocAttrFilterableAttrs)
		if err != nil {
			return err
		}
		if err = c.wait(context.TODO(), "attr", t.TaskUID); err != nil {
			return err
		}
	}
	attrSortAttrs := DocAttrSortAttrs
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

func (c *Client) index(kind string) meilisearch.IndexManager {
	if kind == "attr" {
		return c.attrIndex
	}
	return c.docIndex
}

func (c *Client) CreateDocument(ctx context.Context, doc *doc.Document) error {
	newDoc := (&Document{}).FromModel(doc)
	c.log.Debugf("store entryId %s", newDoc.EntryId)
	task, err := c.index(newDoc.Kind).AddDocuments(newDoc, "id")
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, newDoc.Kind, task.TaskUID); err != nil {
		c.log.Errorf("store document with entryId %s error: %s", newDoc.EntryId, err)
		return err
	}

	// store document attr
	newAttrs := (&DocumentAttrList{}).FromModel(doc)
	c.log.Debugf("store doc of entryId %d attrs: %s", doc.EntryId, newAttrs.String())
	t, err := c.index("attr").AddDocuments(newAttrs, "id")
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, "attr", t.TaskUID); err != nil {
		c.log.Errorf("store document attr of entryId %d error: %s", doc.EntryId, err)
		return err
	}
	return nil
}

func (c *Client) UpdateDocument(ctx context.Context, doc *doc.Document) error {
	// delete document attr
	newAttrsQuery := (&DocumentAttrQuery{}).FromModel(doc)
	c.log.Debugf("delete document attrs: %s", newAttrsQuery.String())

	filter := []interface{}{}
	for _, aq := range newAttrsQuery.AttrQueries {
		filter = append(filter, aq.ToFilter())
	}
	t, err := c.index("attr").DeleteDocumentsByFilter(filter)
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err = c.wait(ctx, "attr", t.TaskUID); err != nil {
		c.log.Errorf("delete document by filter error: %s", err)
		return err
	}
	// store document attr
	newAttrs := (&DocumentAttrList{}).FromModel(doc)
	c.log.Debugf("store doc of entryId %d attrs: %s", doc.EntryId, newAttrs.String())
	t, err = c.index("attr").AddDocuments(newAttrs, "id")
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, "attr", t.TaskUID); err != nil {
		c.log.Errorf("store document attr of entryId %d error: %s", doc.EntryId, err)
		return err
	}
	return nil
}

func (c *Client) GetDocument(ctx context.Context, entryId int64) (*doc.Document, error) {
	namespace := models.GetNamespace(ctx)
	query := (&DocumentQuery{}).OfEntryId(namespace.String(), entryId)
	c.log.Debugf("get document by entryId: %d", entryId)
	rep, err := c.index("document").Search("", query.ToRequest())
	if err != nil {
		return nil, err
	}
	if len(rep.Hits) == 0 {
		return nil, nil
	}
	b, _ := json.Marshal(rep.Hits[0])
	document := &Document{}
	err = json.Unmarshal(b, &document)
	if err != nil {
		return nil, err
	}

	// get attrs
	attrQuery := (&DocumentAttrQuery{}).OfEntryId(document.Namespace, document.EntryId)
	c.log.Debugf("filter document attr: %s", attrQuery.String())
	attrRep, err := c.index("attr").Search("", attrQuery.ToRequest())
	if err != nil {
		return nil, err
	}

	attrs := make([]*DocumentAttr, 0)
	for _, hit := range attrRep.Hits {
		b, _ := json.Marshal(hit)
		attr := &DocumentAttr{}
		err = json.Unmarshal(b, &attr)
		if err != nil {
			c.log.Errorf("unmarshal document attr error: %s", err)
			continue
		}
		attrs = append(attrs, attr)
	}
	return document.ToModel(attrs), nil
}

func (c *Client) FilterDocuments(ctx context.Context, filter *doc.DocumentFilter) ([]*doc.Document, error) {
	query := (&DocumentQuery{}).FromModel(filter)
	if filter.ParentID != nil || filter.Unread != nil || filter.Marked != nil {
		entryIds := make([]string, 0)
		attrQuery := (&DocumentAttrQueries{}).FromFilter(filter)
		for _, aq := range *attrQuery {
			c.log.Debugf("filter document attr: %s", aq.String())
			attrRep, err := c.index("attr").Search("", aq.ToRequest())
			if err != nil {
				return nil, err
			}

			for _, hit := range attrRep.Hits {
				b, _ := json.Marshal(hit)
				attr := &DocumentAttr{}
				err = json.Unmarshal(b, &attr)
				if err != nil {
					c.log.Errorf("unmarshal document attr error: %s", err)
					continue
				}
				entryIds = append(entryIds, attr.EntryId)
			}
		}
		if len(entryIds) != 0 {
			query.AttrQueries = append(query.AttrQueries, &AttrQuery{
				Attr:   "entryId",
				Option: "IN",
				Value:  entryIds,
			})
		}
	}

	c.log.Debugf("search document: [%s] query: [%s]", query.Search, query.String())
	rep, err := c.index("document").Search(query.Search, query.ToRequest())
	if err != nil {
		return nil, err
	}
	c.log.Debugf("query document attr : [%s]", query.String())

	documents := make([]*doc.Document, 0)
	for _, hit := range rep.Hits {
		b, _ := json.Marshal(hit)
		document := &Document{}
		err = json.Unmarshal(b, &document)
		if err != nil {
			c.log.Errorf("unmarshal document error: %s", err)
			continue
		}

		// get attrs
		attrQuery := (&DocumentAttrQuery{}).OfEntryId(document.Namespace, document.EntryId)
		c.log.Debugf("filter document attr: %s", attrQuery.String())
		attrRep, err := c.index("attr").Search("", attrQuery.ToRequest())
		if err != nil {
			return nil, err
		}

		attrs := make([]*DocumentAttr, 0)
		for _, hit := range attrRep.Hits {
			b, _ := json.Marshal(hit)
			attr := &DocumentAttr{}
			err = json.Unmarshal(b, &attr)
			if err != nil {
				c.log.Errorf("unmarshal document attr error: %s", err)
				continue
			}
			attrs = append(attrs, attr)
		}
		documents = append(documents, document.ToModel(attrs))
	}
	return documents, nil
}

func (c *Client) DeleteDocument(ctx context.Context, entryId int64) error {
	c.log.Debugf("delete document by entryId: %d", entryId)
	aq := &AttrQuery{
		Attr:   "entryId",
		Option: "=",
		Value:  fmt.Sprintf("%d", entryId),
	}
	t, err := c.index("attr").DeleteDocumentsByFilter(aq.ToFilter())
	if err != nil {
		c.log.Error(err)
		return err
	}
	if err := c.wait(ctx, "attr", t.TaskUID); err != nil {
		c.log.Errorf("delete document by filter error: %s", err)
	}
	return nil
}

func (c *Client) wait(ctx context.Context, kind string, taskUID int64) error {
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
