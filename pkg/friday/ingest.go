package friday

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"friday/pkg/models"
	"friday/pkg/utils/files"
)

func (f *Friday) Ingest(elements []models.Element) error {
	f.log.Debugf("Ingesting %d ...", len(elements))
	for i, element := range elements {
		// id: sha256(source)-group
		h := sha256.New()
		h.Write([]byte(element.Metadata.Source))
		val := hex.EncodeToString(h.Sum(nil))[:64]
		id := fmt.Sprintf("%s-%s", val, element.Metadata.Group)
		if f.vector.Exist(id) {
			f.log.Debugf("vector %d(th) id(%s) source(%s) exist, skip ...", i, id, element.Metadata.Source)
			continue
		}

		vectors, m, err := f.embedding.VectorQuery(element.Content)
		if err != nil {
			return err
		}

		t := strings.TrimSpace(element.Content)

		metadata := make(map[string]interface{})
		if m != nil {
			metadata = m
		}
		metadata["title"] = element.Metadata.Title
		metadata["source"] = element.Metadata.Source
		metadata["category"] = element.Metadata.Category
		metadata["group"] = element.Metadata.Group
		v := vectors
		f.log.Debugf("store %d(th) vector id (%s) source(%s) ...", i, id, element.Metadata.Source)
		if err := f.vector.Store(id, t, metadata, v); err != nil {
			return err
		}
	}
	return nil
}

func (f *Friday) IngestFromElementFile(ps string) error {
	doc, err := os.ReadFile(ps)
	if err != nil {
		return err
	}
	elements := []models.Element{}
	if err := json.Unmarshal(doc, &elements); err != nil {
		return err
	}
	merged := f.spliter.Merge(elements)
	return f.Ingest(merged)
}

func (f *Friday) IngestFromFile(ps string) error {
	fs, err := files.ReadFiles(ps)
	if err != nil {
		return err
	}

	elements := []models.Element{}
	for n, file := range fs {
		subDocs := f.spliter.Split(file)
		for i, subDoc := range subDocs {
			e := models.Element{
				Content: subDoc,
				Metadata: models.Metadata{
					Source: n,
					Group:  strconv.Itoa(i),
				},
			}
			elements = append(elements, e)
		}
	}

	return f.Ingest(elements)
}
