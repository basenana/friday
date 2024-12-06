package search

import (
	"context"
	"os"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/upsidedown"

	postgres2 "github.com/basenana/friday/pkg/store/vectorstore/postgres"
	"github.com/basenana/friday/pkg/utils/logger"
)

var singleIndex bleve.Index

func InitSearchEngine() error {
	dsn := os.Getenv("PG_DSN")
	fpath, err := initConfigFile(dsn)
	if err != nil {
		return err
	}

	pgCli, err := postgres2.NewPostgresClient(logger.NewLogger("database"), dsn)
	if err != nil {
		return err
	}

	inited, err := pgCli.Inited(context.Background())
	if err != nil {
		return err
	}

	if inited {
		index, err := bleve.OpenUsing(fpath, make(map[string]interface{}))
		if err != nil {
			return err
		}
		singleIndex = index
		return nil
	}

	mapping := bleve.NewIndexMapping()
	documentMapping := bleve.NewDocumentMapping()
	documentMapping.AddFieldMappingsAt("id", bleve.NewNumericFieldMapping())
	documentMapping.AddFieldMappingsAt("title", bleve.NewTextFieldMapping())
	documentMapping.AddFieldMappingsAt("parent_id", bleve.NewNumericFieldMapping())

	documentMapping.AddFieldMappingsAt("html_content", bleve.NewTextFieldMapping())
	documentMapping.AddFieldMappingsAt("keywords", bleve.NewTextFieldMapping())
	documentMapping.AddFieldMappingsAt("created_at", bleve.NewDateTimeFieldMapping())
	documentMapping.AddFieldMappingsAt("updated_at", bleve.NewDateTimeFieldMapping())

	mapping.AddDocumentMapping("document", documentMapping)

	pgConfig := map[string]interface{}{"dsn": dsn}
	index, err := bleve.NewUsing(fpath, mapping, upsidedown.Name, postgres2.PgKVStoreName, pgConfig)
	if err != nil {
		return err
	}
	singleIndex = index
	return nil
}
