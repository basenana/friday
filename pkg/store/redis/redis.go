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

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/redis/rueidis"

	"github.com/basenana/friday/pkg/models/vector"
	"github.com/basenana/friday/pkg/store"
	"github.com/basenana/friday/pkg/utils/files"
	"github.com/basenana/friday/pkg/utils/logger"
)

const (
	EmbeddingPrefix = "friday"
	EmbeddingIndex  = "friday_idx"
)

type RedisClient struct {
	log    logger.Logger
	client rueidis.Client
	prefix string
	index  string
	dim    int
}

var _ store.VectorStore = &RedisClient{}

func NewRedisClientWithDim(redisUrl string, dim int) (store.VectorStore, error) {
	return newRedisClient(redisUrl, EmbeddingPrefix, EmbeddingIndex, dim)
}

func NewRedisClient(redisUrl string) (store.VectorStore, error) {
	return newRedisClient(redisUrl, EmbeddingPrefix, EmbeddingIndex, 1536)
}

func newRedisClient(redisUrl string, prefix, index string, embeddingDim int) (store.VectorStore, error) {
	client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{redisUrl}})
	if err != nil {
		return nil, err
	}
	r := RedisClient{
		log:    logger.NewLogger("redis"),
		client: client,
		prefix: prefix,
		index:  index,
		dim:    embeddingDim,
	}
	err = r.client.Do(context.Background(), r.client.B().FtInfo().Index(index).Build()).Error()
	if err != nil {
		if err.Error() == "Unknown Index name" {
			return &r, r.initIndex()
		}
		return nil, err
	}
	return &r, nil
}

func (r RedisClient) initIndex() error {
	if err := r.client.Do(
		context.Background(),
		r.client.B().Arbitrary("FT.CREATE", r.index, "ON", "HASH", "PREFIX", "1", r.prefix, "SCHEMA").
			Args("id", "TEXT").
			Args("name", "TEXT").
			Args("group", "TEXT").
			Args("extra", "TEXT").
			Args("oid", "TEXT").
			Args("parentid", "TEXT").
			Args("content", "TEXT").
			Args("vector", "VECTOR", "FLAT", "6", "TYPE", "FLOAT32", "DIM", strconv.Itoa(r.dim), "DISTANCE_METRIC", "L2").
			Build()).Error(); err != nil {
		return err
	}
	return nil
}

func (r RedisClient) Store(ctx context.Context, element *vector.Element, extra map[string]any) error {
	if extra == nil {
		extra = make(map[string]interface{})
	}
	extra["group"] = element.Group

	var m string
	b, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	m = string(b)
	return r.client.Do(ctx, r.client.B().Hset().Key(fmt.Sprintf("%s:%s-%d", r.prefix, element.Name, element.Group)).FieldValue().
		FieldValue("id", element.ID).
		FieldValue("name", element.Name).
		FieldValue("group", strconv.Itoa(element.Group)).
		FieldValue("extra", m).
		FieldValue("oid", files.Int64ToStr(element.OID)).
		FieldValue("parentid", files.Int64ToStr(element.ParentId)).
		FieldValue("content", element.Content).
		FieldValue("vector", rueidis.VectorString32(element.Vector)).Build()).Error()
}

func (r RedisClient) Get(ctx context.Context, oid int64, name string, group int) (*vector.Element, error) {
	resp, err := r.client.Do(ctx, r.client.B().Get().Key(fmt.Sprintf("%s:%s-%d", r.prefix, name, group)).Build()).ToMessage()
	if err != nil {
		return nil, err
	}
	res, err := resp.AsStrMap()
	if err != nil {
		return nil, err
	}

	if oid == 0 {
		oid, err = files.StrToInt64(res["oid"])
		if err != nil {
			return nil, err
		}
	}

	parentId, err := files.StrToInt64(res["parentid"])
	if err != nil {
		return nil, err
	}
	return &vector.Element{
		ID:       res["id"],
		Name:     res["name"],
		Group:    group,
		OID:      oid,
		ParentId: parentId,
		Content:  res["content"],
	}, nil
}

func (r RedisClient) Search(ctx context.Context, query vector.VectorDocQuery, vectors []float32, k int) ([]*vector.Doc, error) {
	resp, err := r.client.Do(ctx, r.client.B().FtSearch().Index(r.index).
		Query("*=>[KNN 10 @vector $B AS vector_score]").
		Return("4").Identifier("id").Identifier("content").
		Identifier("metadata").Identifier("vector_score").
		Sortby("vector_score").
		Params().Nargs(2).NameValue().
		NameValue("B", rueidis.VectorString32(vectors)).
		Dialect(2).Build()).ToArray()
	if err != nil {
		return nil, err
	}
	results := make([]*vector.Doc, 0)

	for i := 1; i < len(resp[1:]); i += 2 {
		res, err := resp[i+1].AsStrMap()
		if err != nil {
			return nil, err
		}
		metadata := make(map[string]interface{})
		if err := json.Unmarshal([]byte(res["metadata"]), &metadata); err != nil {
			return nil, err
		}
		oid, err := files.StrToInt64(res["oid"])
		if err != nil {
			return nil, err
		}
		parentId, err := files.StrToInt64(res["parentid"])
		if err != nil {
			return nil, err
		}
		group, err := strconv.Atoi(res["group"])
		if err != nil {
			return nil, err
		}
		results = append(results, &vector.Doc{
			Id:       res["id"],
			OID:      oid,
			Name:     res["name"],
			Group:    group,
			ParentId: parentId,
			Content:  res["content"],
		})
		r.log.Debugf("id: %s, content: %s, score: %s\n", res["id"], res["content"], res["vector_score"])
		if len(results) >= k {
			break
		}
	}
	return results, nil
}
