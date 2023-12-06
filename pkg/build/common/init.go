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

package common

import (
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/build/withvector"
	"github.com/basenana/friday/pkg/friday"
	"github.com/basenana/friday/pkg/utils/logger"
	"github.com/basenana/friday/pkg/vectorstore"
	"github.com/basenana/friday/pkg/vectorstore/pgvector"
	"github.com/basenana/friday/pkg/vectorstore/postgres"
	"github.com/basenana/friday/pkg/vectorstore/redis"
)

func NewFriday(conf *config.Config) (f *friday.Friday, err error) {
	log := conf.Logger
	if conf.Logger == nil {
		log = logger.NewLogger("friday")
	}
	log.SetDebug(conf.Debug)
	conf.Logger = log

	var vectorStore vectorstore.VectorStore
	// init vector store
	if conf.VectorStoreConfig.VectorStoreType == config.VectorStoreRedis {
		if conf.VectorStoreConfig.EmbeddingDim == 0 {
			vectorStore, err = redis.NewRedisClient(conf.VectorStoreConfig.VectorUrl)
			if err != nil {
				return nil, err
			}
		} else {
			vectorStore, err = redis.NewRedisClientWithDim(conf.VectorStoreConfig.VectorUrl, conf.VectorStoreConfig.EmbeddingDim)
			if err != nil {
				return nil, err
			}
		}
	} else if conf.VectorStoreConfig.VectorStoreType == config.VectorStorePGVector {
		vectorStore, err = pgvector.NewPgVectorClient(conf.Logger, conf.VectorStoreConfig.VectorUrl)
		if err != nil {
			return nil, err
		}
	} else if conf.VectorStoreConfig.VectorStoreType == config.VectorStorePostgres {
		vectorStore, err = postgres.NewPostgresClient(conf.Logger, conf.VectorStoreConfig.VectorUrl)
		if err != nil {
			return nil, err
		}
	}

	return withvector.NewFridayWithVector(conf, vectorStore)
}
