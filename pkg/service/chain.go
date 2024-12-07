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

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/dispatch"
	"github.com/basenana/friday/pkg/dispatch/plugin"
	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/store/docstore"
)

type Chain struct {
	MeiliClient docstore.DocStoreInterface
	Plugins     []plugin.ChainPlugin
}

var ChainPool *dispatch.Pool

func NewChain(conf config.Config) (*Chain, error) {
	plugins := []plugin.ChainPlugin{}
	for _, p := range conf.Plugins {
		plugins = append(plugins, plugin.DefaultRegisterer.Get(p))
	}
	client, err := docstore.NewMeiliClient(conf)
	if err != nil {
		return nil, err
	}
	return &Chain{
		MeiliClient: client,
		Plugins:     plugins,
	}, nil
}

func (c *Chain) Store(ctx context.Context, document *doc.Document) error {
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		for _, plugin := range c.Plugins {
			err := plugin.Run(ctx, document)
			if err != nil {
				return err
			}
		}
		return c.MeiliClient.Store(ctx, document)
	})
}

func (c *Chain) StoreAttr(ctx context.Context, docAttr *doc.DocumentAttr) error {
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		if err := c.MeiliClient.DeleteByFilter(ctx, []doc.AttrQuery{
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
		}); err != nil {
			return err
		}
		return c.MeiliClient.Store(ctx, docAttr)
	})
}

func (c *Chain) Search(ctx context.Context, query *doc.DocumentQuery, attrQueries []*doc.DocumentAttrQuery) ([]doc.Document, error) {
	attrs := []doc.DocumentAttr{}
	for _, attrQuery := range attrQueries {
		attr, err := c.MeiliClient.FilterAttr(ctx, attrQuery)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, attr...)
	}
	ids := []string{}
	for _, attr := range attrs {
		ids = append(ids, attr.EntryId)
	}
	if len(ids) == 0 && len(attrQueries) != 0 {
		return []doc.Document{}, nil
	}
	if len(ids) != 0 {
		query.AttrQueries = append(query.AttrQueries, doc.AttrQuery{
			Attr:   "entryId",
			Option: "IN",
			Value:  ids,
		})
	}
	return c.MeiliClient.Search(ctx, query)
}

func (c *Chain) DeleteByFilter(ctx context.Context, queries []doc.AttrQuery) error {
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		return c.MeiliClient.DeleteByFilter(ctx, queries)
	})
}
