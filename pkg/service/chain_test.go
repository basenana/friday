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

package service_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/basenana/friday/pkg/dispatch"
	"github.com/basenana/friday/pkg/dispatch/plugin"
	_ "github.com/basenana/friday/pkg/dispatch/plugin"
	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/service"
	"github.com/basenana/friday/pkg/store/meili"
	"github.com/basenana/friday/pkg/utils/logger"
)

var _ = Describe("Chain", func() {
	var (
		Chain     *service.Chain
		parentId1 = int64(1)
		parentId2 = int64(2)
		entryId11 = int64(11)
		entryId12 = int64(12)
		entryId21 = int64(21)
		doc11     *doc.Document
		doc12     *doc.Document
		doc21     *doc.Document
		t         = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	)

	BeforeEach(func() {
		service.ChainPool = dispatch.NewPool(10)
		logger.InitLog()
		Chain = &service.Chain{
			DocClient: &meili.MockClient{},
			Plugins:   []plugin.ChainPlugin{},
			Log:       logger.NewLog("test"),
		}
		for _, p := range plugin.DefaultRegisterer.Chains {
			Chain.Plugins = append(Chain.Plugins, p)
		}
		doc11 = &doc.Document{
			Namespace:     "test-ns",
			EntryId:       entryId11,
			ParentEntryID: &parentId1,
			Name:          "test-name-11",
			Content:       "<p>test</p><img src=\"http://abc1\"/>",
			CreatedAt:     t,
			ChangedAt:     t,
		}
		doc12 = &doc.Document{
			Namespace:     "test-ns",
			EntryId:       entryId12,
			ParentEntryID: &parentId1,
			Name:          "test-name-12",
			Content:       "<p>test</p><img src=\"http://abc2\"/>",
			CreatedAt:     t,
			ChangedAt:     t,
		}
		doc21 = &doc.Document{
			Namespace:     "test-ns",
			EntryId:       entryId21,
			ParentEntryID: &parentId2,
			Name:          "test-name-21",
			Content:       "<p>test</p><img src=\"http://abc2\"/>",
			CreatedAt:     t,
			ChangedAt:     t,
		}
		err := Chain.CreateDocument(context.TODO(), doc11)
		Expect(err).Should(BeNil())
		err = Chain.CreateDocument(context.TODO(), doc12)
		Expect(err).Should(BeNil())
		err = Chain.CreateDocument(context.TODO(), doc21)
		Expect(err).Should(BeNil())
	})

	Describe("documents", func() {
		Context("store document ", func() {
			It("store document should be successful", func() {
				err := Chain.CreateDocument(context.TODO(), &doc.Document{EntryId: int64(30)})
				Expect(err).Should(BeNil())
			})
			It("store document attr should be successful", func() {
				err := Chain.CreateDocument(context.TODO(), &doc.Document{EntryId: int64(31)})
				Expect(err).Should(BeNil())
			})
		})
		Context("search document", func() {
			It("search document should be successful", func() {
				docs, err := Chain.Search(context.TODO(), &doc.DocumentFilter{
					Search:    "test",
					Namespace: "test-ns",
				})
				Expect(err).Should(BeNil())
				Expect(docs).Should(HaveLen(3))
			})
			It("search document with attr should be successful", func() {
				docs, err := Chain.Search(context.TODO(), &doc.DocumentFilter{
					Search:    "test",
					Namespace: "test-ns",
					ParentID:  &parentId1,
				})
				Expect(err).Should(BeNil())
				Expect(docs).Should(HaveLen(2))
			})
		})
		Context("plugin should work", func() {
			It("header plugin should work", func() {
				doc3 := &doc.Document{
					Namespace: "test-ns",
					EntryId:   int64(100),
					Name:      "test-name-100",
					Content:   "<p>test</p><img src=\"http://abc\"/>",
					CreatedAt: t,
					ChangedAt: t,
				}
				err := Chain.CreateDocument(context.TODO(), doc3)
				Expect(err).Should(BeNil())
				Expect(doc3.HeaderImage).Should(Equal("http://abc"))
			})
		})
		Context("delete document", func() {
			It("delete document by filter should be successful", func() {
				err := Chain.CreateDocument(context.TODO(), &doc.Document{
					Namespace: "test-ns",
					EntryId:   int64(40),
					Name:      "test-name-40",
				})
				Expect(err).Should(BeNil())
				err = Chain.Delete(context.TODO(), "test-ns", int64(40))
				Expect(err).Should(BeNil())
				docs, err := Chain.GetDocument(context.TODO(), "test-ns", int64(40))
				Expect(err).Should(BeNil())
				Expect(docs).Should(BeNil())
			})
		})
	})
})
