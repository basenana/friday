package spliter

import (
	"strings"

	"go.uber.org/zap"

	"friday/pkg/utils/logger"
)

type TextSpliter struct {
	log          *zap.SugaredLogger
	separator    string
	chunkSize    int
	chunkOverlap int
}

var _ Spliter = &TextSpliter{}

func NewTextSpliter(chunkSize int, chunkOverlap int, separator string) Spliter {
	log := logger.NewLogger("text")
	return &TextSpliter{
		log:          log,
		separator:    separator,
		chunkSize:    chunkSize,
		chunkOverlap: chunkOverlap,
	}
}

func (t *TextSpliter) length(d string) int {
	return len(d)
}

func (t *TextSpliter) Split(text string) []string {
	if t.separator == "" {
		return []string{text}
	}
	splits := strings.Split(text, t.separator)
	return t.Merge(splits)
}

func (t *TextSpliter) Merge(splits []string) []string {
	separatorLen := t.length(t.separator)
	docs := []string{}
	current := []string{}
	total := 0
	for _, d := range splits {
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
