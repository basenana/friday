package search

import (
	"fmt"
	"github.com/basenana/friday/pkg/models"
	"github.com/blevesearch/bleve/v2"
	"github.com/google/uuid"
	"testing"
	"time"
)

func Test_doSearch(t *testing.T) {
	err := InitSearchEngine()
	if err != nil {
		t.Errorf("init index mapping failed: %s", err)
		return
	}

	t.Log(singleIndex.DocCount())

	for i := 0; i < 1; i++ {
		doc := &models.Document{
			ID: 10011 + time.Now().UnixNano(),
			//Title: uuid.New().String(),
			Title:       "hello world",
			ParentID:    1,
			HtmlContent: uuid.New().String(),
			Keywords:    "test",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = singleIndex.Index(fmt.Sprintf("d%d", doc.ID), doc)
	}

	// search for some text
	query := bleve.NewMatchQuery("hello")
	search := bleve.NewSearchRequest(query)
	search.Size = 100
	searchResults, err := singleIndex.Search(search)
	if err != nil {
		t.Errorf("search index failed: %s", err)
		return
	}
	t.Logf("resule: %+v", searchResults)
	for _, r := range searchResults.Hits {
		singleIndex.Delete(r.ID)
	}
}
