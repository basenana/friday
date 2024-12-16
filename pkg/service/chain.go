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
	"fmt"

	"go.uber.org/zap"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/dispatch"
	"github.com/basenana/friday/pkg/dispatch/plugin"
	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/store"
	"github.com/basenana/friday/pkg/store/meili"
	"github.com/basenana/friday/pkg/store/postgres"
	"github.com/basenana/friday/pkg/utils/logger"
)

type Chain struct {
	DocClient store.DocStoreInterface
	Plugins   []plugin.ChainPlugin
	Log       *zap.SugaredLogger
}

var ChainPool *dispatch.Pool

func NewChain(conf config.Config) (*Chain, error) {
	plugins := []plugin.ChainPlugin{}
	for _, p := range conf.Plugins {
		plugins = append(plugins, plugin.DefaultRegisterer.Get(p))
	}
	log := logger.NewLog("chain")
	var (
		client store.DocStoreInterface
		err    error
	)
	switch conf.DocStore.Type {
	case "meili":
		client, err = meili.NewMeiliClient(conf)
		if err != nil {
			log.Errorf("new meili client error: %s", err)
			return nil, err
		}
	case "postgres":
		client, err = postgres.NewPostgresClient(conf.DocStore.PostgresConfig.DSN)
		if err != nil {
			log.Errorf("new postgres client error: %s", err)
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported docstore type: %s", conf.DocStore.Type)
	}
	return &Chain{
		DocClient: client,
		Plugins:   plugins,
		Log:       log,
	}, nil
}

func (c *Chain) CreateDocument(ctx context.Context, document *doc.Document) error {
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		ctx = c.WithNamespace(ctx, document.Namespace)
		c.Log.Debugf("create document od entryId: %d", document.EntryId)
		if d, err := c.GetDocument(ctx, document.Namespace, document.EntryId); err != nil && err != models.ErrNotFound {
			c.Log.Errorf("get document error: %s", err)
			return err
		} else if d != nil {
			c.Log.Debugf("document already exists: %s", d.Name)
			return fmt.Errorf("document already exists: %s", d.Name)
		}
		for _, plugin := range c.Plugins {
			err := plugin.Run(ctx, document)
			if err != nil {
				c.Log.Errorf("plugin error: %s", err)
				return err
			}
		}
		c.Log.Debugf("create document: %+v", document.Name)
		return c.DocClient.CreateDocument(ctx, document)
	})
}

func (c *Chain) UpdateDocument(ctx context.Context, document *doc.Document) error {
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		ctx = c.WithNamespace(ctx, document.Namespace)
		c.Log.Debugf("update document of entryId: %d", document.EntryId)
		return c.DocClient.UpdateDocument(ctx, document)
	})
}

func (c *Chain) GetDocument(ctx context.Context, namespace string, entryId int64) (*doc.Document, error) {
	c.Log.Debugf("get document: namespace=%s, entryId=%d", namespace, entryId)
	ctx = c.WithNamespace(ctx, namespace)
	doc, err := c.DocClient.GetDocument(ctx, entryId)
	if err != nil {
		c.Log.Errorf("get document error: %s", err)
		return nil, err
	}
	c.Log.Debugf("get document of entryId: %d", entryId)
	return doc, nil
}

func (c *Chain) Search(ctx context.Context, filter *doc.DocumentFilter) ([]*doc.Document, error) {
	ctx = c.WithNamespace(ctx, filter.Namespace)
	c.Log.Debugf("search document: %+v", filter.String())
	return c.DocClient.FilterDocuments(ctx, filter)
}

func (c *Chain) Delete(ctx context.Context, namespace string, entryId int64) error {
	ctx = c.WithNamespace(ctx, namespace)
	return ChainPool.Run(ctx, func(ctx context.Context) error {
		c.Log.Debugf("delete document of entryId: %d", entryId)
		err := c.DocClient.DeleteDocument(ctx, entryId)
		if err != nil {
			c.Log.Errorf("delete document of entryId %d error: %s", entryId, err)
			return err
		}
		return nil
	})
}

func (c *Chain) WithNamespace(ctx context.Context, namespace string) context.Context {
	return models.WithNamespace(ctx, models.NewNamespace(namespace))
}
