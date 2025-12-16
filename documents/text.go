package documents

import (
	"fmt"
	"github.com/basenana/friday/types"
	"strings"

	"github.com/basenana/friday/utils"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

const (
	DefaultChunkSize    = 500
	DefaultChunkOverlap = 100
)

type TextSpliter struct {
	separator    string
	chunkSize    int
	chunkOverlap int
	log          *zap.SugaredLogger
}

func NewTextSpliter(chunkSize int, chunkOverlap int, separator string) *TextSpliter {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	if chunkOverlap <= 0 {
		chunkOverlap = DefaultChunkOverlap
	}
	if separator == "" {
		separator = "\n"
	}
	return &TextSpliter{
		log:          logger.New("documents.TextSpliter"),
		separator:    separator,
		chunkSize:    chunkSize,
		chunkOverlap: chunkOverlap,
	}
}

func (t *TextSpliter) length(d string) int {
	return Length(d)
}

func (t *TextSpliter) Split(text string) []string {
	if t.separator == "" {
		return []string{text}
	}
	splits := strings.Split(text, t.separator)
	return t.merge(splits)
}

func (t *TextSpliter) merge(splits []string) []string {
	separatorLen := t.length(t.separator)
	docs := []string{}
	current := []string{}
	total := 0
	for _, d := range splits {
		if len(d) == 0 {
			continue
		}
		l := t.length(d)
		sLen := separatorLen
		if len(current) == 0 {
			sLen = 0
		}
		if total+sLen+l > t.chunkSize {
			if total > t.chunkSize {
				t.log.Warnf("Created a chunk of size %d, which is longer than the specified %d", total, t.chunkSize)
			}
			if len(current) > 0 {
				doc := t.join(current)
				if doc != "" {
					docs = append(docs, doc)
				}
				for total > t.chunkOverlap || total+l+sLen > t.chunkSize && total > 0 {
					total -= t.length(current[0]) + separatorLen
					current = current[1:]
				}
			}
		}
		current = append(current, d)
		total += l + sLen
	}
	doc := t.join(current)
	if doc != "" {
		docs = append(docs, doc)
	}
	return docs
}

func (t *TextSpliter) join(docs []string) string {
	return strings.TrimSpace(strings.Join(docs, t.separator))
}

func SplitTextContent(chunkType string, metadata map[string]string, content string, config SplitConfig) []*types.Chunk {
	var (
		chunks  []*types.Chunk
		docHash = utils.ComputeStructHash(content, nil)
		idx     int
	)

	sp := NewTextSpliter(config.Size, config.Overlap, "\n")
	chunkContents := sp.Split(content)
	for _, chunk := range chunkContents {
		c := &types.Chunk{
			Type: chunkType,
			Metadata: map[string]string{
				types.MetadataChunkDocument: docHash,
				types.MetadataChunkIndex:    fmt.Sprintf("%d", idx),
			},
			Content: chunk,
		}

		for k, v := range metadata {
			c.Metadata[k] = v
		}

		chunks = append(chunks, c)
		idx++
	}
	return chunks
}

type SplitConfig struct {
	Size    int
	Overlap int
}
