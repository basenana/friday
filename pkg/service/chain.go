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

package service

import (
	"context"

	"go.uber.org/zap"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/dispatch"
	"github.com/basenana/friday/pkg/dispatch/plugin"
	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/store/docstore"
	"github.com/basenana/friday/pkg/utils/logger"
)

type Chain struct {
	MeiliClient docstore.DocStoreInterface
	Plugins     []plugin.ChainPlugin
	Log         *zap.SugaredLogger
}

var ChainPool *dispatch.Pool

func NewChain(conf config.Config) (*Chain, error) {
	plugins := []plugin.ChainPlugin{}
	for _, p := range conf.Plugins {
		plugins = append(plugins, plugin.DefaultRegisterer.Get(p))
	}
	log := logger.NewLog("chain")
	client, err := docstore.NewMeiliClient(conf)
	if err != nil {
		log.Errorf("new meili client error: %s", err)
		return nil, err
	}
	return &Chain{
		MeiliClient: client,
		Plugins:     plugins,
		Log:         log,
	}, nil
}

func (c *Chain) Store(ctx context.Context, document *doc.Document) error {
	document.Kind = "document"
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		c.Log.Debugf("store document: %+v", document.String())
		if d, err := c.GetDocument(ctx, document.Namespace, document.EntryId); err != nil {
			c.Log.Errorf("get document error: %s", err)
			return err
		} else if d != nil {
			c.Log.Debugf("document already exists: %+v", d.String())
			return nil
		}
		for _, plugin := range c.Plugins {
			err := plugin.Run(ctx, document)
			if err != nil {
				c.Log.Errorf("plugin error: %s", err)
				return err
			}
		}
		c.Log.Debugf("store document: %+v", document.String())
		return c.MeiliClient.Store(ctx, document)
	})
}

func (c *Chain) StoreAttr(ctx context.Context, docAttr *doc.DocumentAttr) error {
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		if err := c.MeiliClient.DeleteByFilter(ctx, doc.DocumentAttrQuery{
			AttrQueries: []*doc.AttrQuery{
				{
					Attr:   "namespace",
					Option: "=",
					Value:  docAttr.Namespace,
				},
				{
					Attr:   "key",
					Option: "=",
					Value:  docAttr.Key,
				},
				{
					Attr:   "entryId",
					Option: "=",
					Value:  docAttr.EntryId,
				},
				{
					Attr:   "kind",
					Option: "=",
					Value:  "attr",
				}},
		}); err != nil {
			c.Log.Errorf("delete document attr error: %s", err)
			return err
		}
		docAttr.Kind = "attr"
		c.Log.Debugf("store attr: %+v", docAttr.String())
		return c.MeiliClient.Store(ctx, docAttr)
	})
}

func (c *Chain) GetDocument(ctx context.Context, namespace, entryId string) (*doc.Document, error) {
	c.Log.Debugf("get document: namespace=%s, entryId=%s", namespace, entryId)
	docs, err := c.MeiliClient.Search(ctx, &doc.DocumentQuery{
		AttrQueries: []*doc.AttrQuery{
			{
				Attr:   "namespace",
				Option: "=",
				Value:  namespace,
			},
			{
				Attr:   "entryId",
				Option: "=",
				Value:  entryId,
			},
			{
				Attr:   "kind",
				Option: "=",
				Value:  "document",
			},
		},
		Search:      "",
		HitsPerPage: 1,
		Page:        1,
	})
	if err != nil {
		c.Log.Errorf("get document error: %s", err)
		return nil, err
	}
	if len(docs) == 0 {
		c.Log.Debugf("document not found: namespace=%s, entryId=%s", namespace, entryId)
		return nil, nil
	}
	c.Log.Debugf("get document: %+v", docs[0].String())
	return docs[0], nil
}

func (c *Chain) ListDocumentAttrs(ctx context.Context, namespace string, entryIds []string) (doc.DocumentAttrList, error) {
	docAttrQuery := &doc.DocumentAttrQuery{
		AttrQueries: []*doc.AttrQuery{
			{
				Attr:   "namespace",
				Option: "=",
				Value:  namespace,
			},
			{
				Attr:   "entryId",
				Option: "IN",
				Value:  entryIds,
			},
			{
				Attr:   "kind",
				Option: "=",
				Value:  "attr",
			},
		},
	}
	c.Log.Debugf("list document attrs: %+v", docAttrQuery.String())
	attrs, err := c.MeiliClient.FilterAttr(ctx, docAttrQuery)
	if err != nil {
		c.Log.Errorf("list document attrs error: %s", err)
		return nil, err
	}
	c.Log.Debugf("list %d document attrs: %s", len(attrs), attrs.String())
	return attrs, nil
}

func (c *Chain) GetDocumentAttrs(ctx context.Context, namespace, entryId string) ([]*doc.DocumentAttr, error) {
	docAttrQuery := &doc.DocumentAttrQuery{
		AttrQueries: []*doc.AttrQuery{
			{
				Attr:   "namespace",
				Option: "=",
				Value:  namespace,
			},
			{
				Attr:   "entryId",
				Option: "=",
				Value:  entryId,
			},
			{
				Attr:   "kind",
				Option: "=",
				Value:  "attr",
			},
		},
	}
	c.Log.Debugf("get document attrs: %+v", docAttrQuery.String())
	attrs, err := c.MeiliClient.FilterAttr(ctx, docAttrQuery)
	if err != nil {
		c.Log.Errorf("get document attrs error: %s", err)
		return nil, err
	}
	c.Log.Debugf("get %d document attrs: %s", len(attrs), attrs.String())
	return attrs, nil
}

func (c *Chain) Search(ctx context.Context, query *doc.DocumentQuery, attrQueries []*doc.DocumentAttrQuery) ([]*doc.Document, error) {
	attrs := doc.DocumentAttrList{}
	for _, attrQuery := range attrQueries {
		attrQuery.AttrQueries = append(attrQuery.AttrQueries, &doc.AttrQuery{
			Attr:   "kind",
			Option: "=",
			Value:  "attr",
		})
		c.Log.Debugf("filter attr query: %+v", attrQuery.String())
		attr, err := c.MeiliClient.FilterAttr(ctx, attrQuery)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, attr...)
	}
	c.Log.Debugf("filter %d attrs: %s", len(attrs), attrs.String())
	ids := []string{}
	for _, attr := range attrs {
		ids = append(ids, attr.EntryId)
	}
	if len(ids) == 0 && len(attrQueries) != 0 {
		return nil, nil
	}

	query.AttrQueries = append(query.AttrQueries, &doc.AttrQuery{
		Attr:   "kind",
		Option: "=",
		Value:  "document",
	})
	if len(ids) != 0 {
		query.AttrQueries = append(query.AttrQueries, &doc.AttrQuery{
			Attr:   "entryId",
			Option: "IN",
			Value:  ids,
		})
	}
	c.Log.Debugf("search document query: %+v", query.String())
	docs, err := c.MeiliClient.Search(ctx, query)
	if err != nil {
		c.Log.Errorf("search document error: %s", err)
		return nil, err
	}
	c.Log.Debugf("search %d documents: %s", len(docs), docs.String())
	return docs, nil
}

func (c *Chain) DeleteByFilter(ctx context.Context, queries doc.DocumentAttrQuery) error {
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		c.Log.Debugf("delete by filter: %+v", queries.String())
		err := c.MeiliClient.DeleteByFilter(ctx, queries)
		if err != nil {
			c.Log.Errorf("delete by filter error: %s", err)
			return err
		}
		return nil
	})
}
