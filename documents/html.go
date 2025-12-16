package documents

import (
	"github.com/basenana/friday/types"
	"regexp"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

func SplitHTMLContent(chunkType string, metadata map[string]string, content string, config SplitConfig) []*types.Chunk {
	markdown, err := htmltomarkdown.ConvertString(content)
	if err != nil {
		return SplitTextContent(chunkType, metadata, HTMLContentTrim(content), config)
	}
	return SplitTextContent(chunkType, metadata, markdown, config)
}

var repeatSpace = regexp.MustCompile(`\s+`)
var htmlCharFilterRegexp = regexp.MustCompile(`</?[!\w:]+((\s+[\w-]+(\s*=\s*(?:\\*".*?"|'.*?'|[^'">\s]+))?)+\s*|\s*)/?>`)

func HTMLContentTrim(content string) string {
	content = strings.ReplaceAll(content, "</p>", "</p>\n")
	content = strings.ReplaceAll(content, "</P>", "</P>\n")
	content = strings.ReplaceAll(content, "</div>", "</div>\n")
	content = strings.ReplaceAll(content, "</DIV>", "</DIV>\n")
	content = htmlCharFilterRegexp.ReplaceAllString(content, "")
	content = repeatSpace.ReplaceAllString(content, " ")
	return content
}
