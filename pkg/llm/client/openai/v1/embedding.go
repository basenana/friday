package v1

import (
	"bytes"
	"encoding/json"
)

type EmbeddingResult struct {
	Object string         `json:"object"`
	Data   []Embeddings   `json:"data"`
	Model  string         `json:"model"`
	Usage  map[string]int `json:"usage"`
}

type EmbeddingData struct {
	Object    string    `json:"object"`
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type Embeddings struct {
	Embedding []float32 `json:"embedding"`
}

func (o *OpenAIV1) Embedding(doc string) ([]float32, error) {
	path := "embeddings"

	model := "text-embedding-ada-002"
	data := map[string]string{
		"model": model,
		"input": doc,
	}
	postBody, _ := json.Marshal(data)

	respBody, err := o.request(path, "POST", bytes.NewBuffer(postBody))
	if err != nil {
		return nil, err
	}

	var res EmbeddingResult
	err = json.Unmarshal(respBody, &res)
	return res.Data[0].Embedding, err
}
