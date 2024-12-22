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
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/basenana/friday/pkg/models/doc"
)

type SubContentPlugin struct {
}

func (s *SubContentPlugin) Name() string {
	return "subContent"
}

func (s *SubContentPlugin) Run(ctx context.Context, doc *doc.Document) error {
	doc.SubContent = GenerateContentSubContent(doc.Content)
	return nil
}

var _ ChainPlugin = &SubContentPlugin{}

func GenerateContentSubContent(content string) string {
	if subContent, err := slowPathContentSubContent([]byte(content)); err == nil {
		return subContent
	}

	content = ContentTrim("html", content)
	subContents := strings.Split(content, "\n")
	contents := make([]string, 0)
	i := 0
	for _, subContent := range subContents {
		subContent = strings.TrimSpace(subContent)
		if subContent != "" {
			contents = append(contents, subContent)
			i++
			if i >= 3 {
				break
			}
		}
	}
	return strings.Join(contents, " ")
}

func slowPathContentSubContent(content []byte) (string, error) {
	query, err := goquery.NewDocumentFromReader(bytes.NewReader(content))
	if err != nil {
		return "", err
	}

	contents := make([]string, 0)
	query.Find("p").EachWithBreak(func(i int, selection *goquery.Selection) bool {
		if len(contents) > 10 {
			return false
		}
		t := strings.TrimSpace(selection.Text())
		if t != "" {
			contents = append(contents, strings.ReplaceAll(t, "\n", " "))
		}
		return true
	})

	return trimDocumentContent(strings.Join(contents, " "), 400), nil
}

func trimDocumentContent(str string, m int) string {
	str = ContentTrim("html", str)
	runes := []rune(str)
	if len(runes) > m {
		return string(runes[:m])
	}
	return str
}

func init() {
	RegisterPlugin(&SubContentPlugin{})
}
