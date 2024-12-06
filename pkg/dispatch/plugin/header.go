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

	"github.com/PuerkitoBio/goquery"

	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/utils"
)

type HeaderImgPlugin struct {
}

func (h *HeaderImgPlugin) Name() string {
	return "headerImg"
}

func (h *HeaderImgPlugin) Run(ctx context.Context, doc *doc.Document) error {
	var headerImgUrl string
	query, err := goquery.NewDocumentFromReader(bytes.NewReader([]byte(doc.Content)))
	if err != nil {
		return fmt.Errorf("build doc query with id %s error: %s", doc.Id, err)
	}

	query.Find("img").EachWithBreak(func(i int, selection *goquery.Selection) bool {
		var (
			srcVal    string
			isExisted bool
		)
		srcVal, isExisted = selection.Attr("src")
		if isExisted && utils.IsUrl(srcVal) {
			headerImgUrl = srcVal
			return false
		}
		srcVal, isExisted = selection.Attr("data-src")
		if isExisted && utils.IsUrl(srcVal) {
			headerImgUrl = srcVal
			return false
		}
		srcVal, isExisted = selection.Attr("data-src-retina")
		if isExisted && utils.IsUrl(srcVal) {
			headerImgUrl = srcVal
			return false
		}
		srcVal, isExisted = selection.Attr("data-original")
		if isExisted && utils.IsUrl(srcVal) {
			headerImgUrl = srcVal
			return false
		}
		return true
	})
	doc.HeaderImage = headerImgUrl
	return nil
}

func init() {
	RegisterPlugin(&HeaderImgPlugin{})
}
