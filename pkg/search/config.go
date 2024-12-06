package search

import (
	"encoding/json"
	"os"
	"path"

	"github.com/blevesearch/bleve/v2/index/upsidedown"

	"github.com/basenana/friday/pkg/store/vectorstore/postgres"
)

func initConfigFile(dsn string) (string, error) {
	cfg := IndexMeta{
		Storage:   postgres.PgKVStoreName,
		IndexType: upsidedown.Name,
		Config:    map[string]interface{}{"dsn": dsn},
	}

	cfgDir, err := os.MkdirTemp(os.TempDir(), "friday_bleve_")
	if err != nil {
		return "", err
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}

	err = os.WriteFile(path.Join(cfgDir, "index_meta.json"), raw, 0655)
	if err != nil {
		return "", err
	}
	return cfgDir, nil
}

type IndexMeta struct {
	Storage   string                 `json:"storage"`
	IndexType string                 `json:"index_type"`
	Config    map[string]interface{} `json:"config,omitempty"`
}
