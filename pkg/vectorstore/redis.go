package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/rueian/rueidis"
	"go.uber.org/zap"
)

const (
	EmbeddingPrefix = "friday"
	EmbeddingIndex  = "friday_idx"
)

type RedisClient struct {
	log    *zap.SugaredLogger
	client rueidis.Client
	prefix string
	index  string
	dim    int
}

var _ VectorStore = &RedisClient{}

func NewRedisClientWithDim(redisUrl string, dim int) (VectorStore, error) {
	return newRedisClient(redisUrl, EmbeddingPrefix, EmbeddingIndex, dim)
}

func NewRedisClient(redisUrl string) (VectorStore, error) {
	return newRedisClient(redisUrl, EmbeddingPrefix, EmbeddingIndex, 1536)
}

func newRedisClient(redisUrl string, prefix, index string, embeddingDim int) (VectorStore, error) {
	client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{redisUrl}})
	if err != nil {
		return nil, err
	}
	r := RedisClient{client: client, prefix: prefix, index: index, dim: embeddingDim}
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
			Args("metadata", "TEXT").
			Args("content", "TEXT").
			Args("vector", "VECTOR", "FLAT", "6", "TYPE", "FLOAT32", "DIM", strconv.Itoa(r.dim), "DISTANCE_METRIC", "L2").
			Build()).Error(); err != nil {
		return err
	}
	return nil
}

func (r RedisClient) Store(id, content string, metadata map[string]interface{}, vectors []float32) error {
	ctx := context.Background()

	var m string
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		m = string(b)
	}
	return r.client.Do(ctx, r.client.B().Hset().Key(fmt.Sprintf("%s:%s", r.prefix, id)).FieldValue().
		FieldValue("id", id).
		FieldValue("metadata", m).
		FieldValue("content", content).
		FieldValue("vector", rueidis.VectorString32(vectors)).Build()).Error()
}

func (r RedisClient) Search(vectors []float32, k int) ([]Doc, error) {
	ctx := context.Background()

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
	results := make([]Doc, 0)

	for i := 1; i < len(resp[1:]); i += 2 {
		res, err := resp[i+1].AsStrMap()
		if err != nil {
			return nil, err
		}
		metadata := make(map[string]interface{})
		if err := json.Unmarshal([]byte(res["metadata"]), &metadata); err != nil {
			return nil, err
		}
		results = append(results, Doc{
			Id:       res["id"],
			Metadata: metadata,
			Content:  res["content"],
		})
		//fmt.Printf("id: %s, content: %s, score: %s\n", res["id"], res["content"], res["vector_score"])
		if len(results) >= k {
			break
		}
	}
	return results, nil
}
