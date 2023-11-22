package search

import (
	"context"
	"github.com/basenana/friday/pkg/vectorstore/postgres"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/upsidedown"
	"os"
)

var singleIndex bleve.Index

func InitSearchEngine() error {
	dsn := os.Getenv("PG_DSN")
	fpath, err := initConfigFile(dsn)
	if err != nil {
		return err
	}

	pgCli, err := postgres.NewPostgresClient(dsn)
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
	index, err := bleve.NewUsing(fpath, mapping, upsidedown.Name, postgres.PgKVStoreName, pgConfig)
	if err != nil {
		return err
	}
	singleIndex = index
	return nil
}
