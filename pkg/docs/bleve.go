package docs

import (
	"fmt"
	"strings"

	"github.com/blevesearch/bleve"
	"go.uber.org/zap"

	"friday/pkg/models"
	"friday/pkg/utils/logger"
)

const DefaultIndexname = "friday.bleve"

type BleveClient struct {
	log       *zap.SugaredLogger
	IndexName string
}

func NewBleveClient(indexName string) (*BleveClient, error) {
	indexSession, err := bleve.Open(indexName)
	if err == bleve.ErrorIndexPathDoesNotExist {
		mapping := bleve.NewIndexMapping()
		indexSession, err = bleve.New(indexName, mapping)
		if err != nil {
			return nil, err
		}

		//contentMapping := bleve.NewDocumentMapping()
		//contentMapping.Dynamic = false
		//
		//// source data store - this is where original doc will be stored
		//dataTextFieldMapping := bleve.NewTextFieldMapping()
		//dataTextFieldMapping.Store = true
		//dataTextFieldMapping.Index = false
		//dataTextFieldMapping.IncludeInAll = false
		//dataTextFieldMapping.IncludeTermVectors = false
		//dataTextFieldMapping.IncludeInAll = false
		//contentMapping.AddFieldMappingsAt("data", dataTextFieldMapping)
		//
		//// create
		//indexMapping := bleve.NewIndexMapping()
		//indexMapping.AddDocumentMapping("content", contentMapping)
		//indexMapping.TypeField = "type"
		//indexMapping.DefaultAnalyzer = "en"
		//index, err = bleve.New(indexName, indexMapping)
		////index.Close()
		//if err != nil {
		//	return nil, err
		//}

	} else if err != nil {
		return nil, err
	}

	defer indexSession.Close()
	return &BleveClient{
		log:       logger.NewLogger("bleve"),
		IndexName: indexName,
	}, nil
}

func (b *BleveClient) IndexDoc(doc models.Element) error {
	indexSession, err := bleve.Open(b.IndexName)
	if err != nil {
		return err
	}
	defer indexSession.Close()
	id := fmt.Sprintf("%s-%s", doc.Metadata.Title, doc.Metadata.Group)
	return indexSession.Index(id, doc)
}

func (b *BleveClient) IndexDocByGroup(docs []models.Element) error {
	indexSession, err := bleve.Open(b.IndexName)
	if err != nil {
		return err
	}
	defer indexSession.Close()
	groups := make(map[string]models.Element)
	for _, doc := range docs {
		id := fmt.Sprintf("%s-%s", doc.Metadata.Title, doc.Metadata.Group)
		group, ok := groups[id]
		if !ok {
			group = models.Element{
				Content:  doc.Content,
				Metadata: doc.Metadata,
			}
		}
		group.Content = strings.Join([]string{group.Content, doc.Content}, "\n")
		groups[id] = group
	}
	for id, doc := range groups {
		if err = indexSession.Index(id, doc); err != nil {
			return err
		}
	}
	return nil
}

func (b *BleveClient) Search(q string) (model *models.Element, err error) {
	indexSession, err := bleve.Open(b.IndexName)
	if err != nil {
		return nil, err
	}
	defer indexSession.Close()

	query := bleve.NewQueryStringQuery(q)
	searchRequest := bleve.NewSearchRequest(query)
	searchResult, err := indexSession.Search(searchRequest)
	if err != nil {
		return nil, err
	}
	if searchResult.Total == 0 {
		return nil, nil
	}

	if len(searchResult.Hits) == 0 {
		return nil, nil
	}

	hit := searchResult.Hits[0]
	doc, err := indexSession.Document(hit.ID)
	var (
		content  string
		title    string
		group    string
		category string
	)
	for _, field := range doc.Fields {
		if field.Name() == "content" {
			content = string(field.Value())
		}
		if field.Name() == "metadata.title" {
			title = string(field.Value())
		}
		if field.Name() == "metadata.group" {
			group = string(field.Value())
		}
		if field.Name() == "metadata.category" {
			category = string(field.Value())
		}
	}
	m := &models.Element{
		Content: content,
		Metadata: models.Metadata{
			Title:    title,
			Group:    group,
			Category: category,
		},
	}

	return m, nil

	//searchRequest := bleve.NewSearchRequest(query)
	//searchRequest.Fields = []string{"data"}
	//searchResp, err := b.Index.Search(searchRequest)
	//if err != nil {
	//	panic(err)
	//}
	//fmt.Println(searchResp)
	// now you can further parse data...
}
