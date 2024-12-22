/*
 Copyright 2024 Friday Author.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package plugin

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/hyponet/jiebago"

	"github.com/basenana/friday/pkg/models/doc"
)

type DocProcessPlugin struct {
	seg jiebago.Segmenter
}

func NewDocProcessPlugin() *DocProcessPlugin {
	seg := jiebago.Segmenter{}
	seg.LoadDictionary("dict.txt")
	return &DocProcessPlugin{
		seg: seg,
	}
}

var _ ChainPlugin = &DocProcessPlugin{}

func (s *DocProcessPlugin) Name() string {
	return "docProcess"
}

func (s *DocProcessPlugin) Run(ctx context.Context, doc *doc.Document) error {
	var err error
	// html analysis
	doc.PureContent, err = trimContent(doc.Content)
	if err != nil {
		return fmt.Errorf("process doc with id %d error: %s", doc.EntryId, err)
	}

	// split title
	doc.TitleTokens = s.splitTokens(doc.Name)

	// split content
	doc.ContentTokens = s.splitTokens(doc.PureContent)
	return nil
}

func trimContent(content string) (string, error) {
	query, err := goquery.NewDocumentFromReader(bytes.NewReader([]byte(content)))
	if err != nil {
		return "", err
	}

	query.Find("body").EachWithBreak(func(i int, selection *goquery.Selection) bool {
		t := strings.TrimSpace(selection.Text())
		if t != "" {
			content = t
		}
		return true
	})

	content = ContentTrim("html", content)
	content = strings.ReplaceAll(content, "'", "")
	return content, nil
}

func (s *DocProcessPlugin) splitTokens(content string) []string {
	contentCh := s.seg.CutForSearch(content, true)
	tokens := make([]string, 0, len(contentCh))
	for token := range contentCh {
		tokens = append(tokens, token)
	}
	return tokens
}

func init() {
	RegisterPlugin(NewDocProcessPlugin())
}
