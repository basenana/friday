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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/basenana/friday/pkg/dispatch"
	"github.com/basenana/friday/pkg/dispatch/plugin"
	_ "github.com/basenana/friday/pkg/dispatch/plugin"
	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/service"
	"github.com/basenana/friday/pkg/store/docstore"
	"github.com/basenana/friday/pkg/utils/logger"
)

var _ = Describe("Chain", func() {
	var (
		Chain     *service.Chain
		parentId1 = "1"
		parentId2 = "2"
		entryId11 = "11"
		entryId12 = "12"
		entryId21 = "21"
		doc11     *doc.Document
		doc12     *doc.Document
		doc21     *doc.Document
		attr11    *doc.DocumentAttr
		attr12    *doc.DocumentAttr
		attr21    *doc.DocumentAttr
	)

	BeforeEach(func() {
		service.ChainPool = dispatch.NewPool(10)
		logger.InitLog()
		Chain = &service.Chain{
			MeiliClient: &docstore.MockClient{},
			Plugins:     []plugin.ChainPlugin{},
			Log:         logger.NewLog("test"),
		}
		for _, p := range plugin.DefaultRegisterer.Chains {
			Chain.Plugins = append(Chain.Plugins, p)
		}
		doc11 = &doc.Document{
			Id:        "1",
			Namespace: "test-ns",
			EntryId:   entryId11,
			Name:      "test-name-11",
			Kind:      "document",
			Content:   "<p>test</p><img src=\"http://abc1\"/>",
			CreatedAt: 1733584543,
			UpdatedAt: 1733584543,
		}
		doc12 = &doc.Document{
			Id:        "2",
			Namespace: "test-ns",
			EntryId:   entryId12,
			Name:      "test-name-12",
			Kind:      "document",
			Content:   "<p>test</p><img src=\"http://abc2\"/>",
			CreatedAt: 1733584543,
			UpdatedAt: 1733584543,
		}
		doc21 = &doc.Document{
			Id:        "3",
			Namespace: "test-ns",
			EntryId:   entryId21,
			Name:      "test-name-21",
			Kind:      "document",
			Content:   "<p>test</p><img src=\"http://abc2\"/>",
			CreatedAt: 1733584543,
			UpdatedAt: 1733584543,
		}
		attr11 = &doc.DocumentAttr{
			Id:        "4",
			Namespace: "test-ns",
			Kind:      "attr",
			EntryId:   entryId11,
			Key:       "parentId",
			Value:     parentId1,
		}
		attr12 = &doc.DocumentAttr{
			Id:        "5",
			Namespace: "test-ns",
			Kind:      "attr",
			EntryId:   entryId12,
			Key:       "parentId",
			Value:     parentId1,
		}
		attr21 = &doc.DocumentAttr{
			Id:        "6",
			Namespace: "test-ns",
			Kind:      "attr",
			EntryId:   entryId21,
			Key:       "parentId",
			Value:     parentId2,
		}
		err := Chain.Store(context.TODO(), doc11)
		Expect(err).Should(BeNil())
		err = Chain.Store(context.TODO(), doc12)
		Expect(err).Should(BeNil())
		err = Chain.Store(context.TODO(), doc21)
		Expect(err).Should(BeNil())
		err = Chain.StoreAttr(context.TODO(), attr11)
		Expect(err).Should(BeNil())
		err = Chain.StoreAttr(context.TODO(), attr12)
		Expect(err).Should(BeNil())
		err = Chain.StoreAttr(context.TODO(), attr21)
		Expect(err).Should(BeNil())
	})

	Describe("documents", func() {
		Context("store document ", func() {
			It("store document should be successful", func() {
				err := Chain.Store(context.TODO(), &doc.Document{Id: "10"})
				Expect(err).Should(BeNil())
			})
			It("store document attr should be successful", func() {
				err := Chain.StoreAttr(context.TODO(), &doc.DocumentAttr{Id: "11"})
				Expect(err).Should(BeNil())
			})
		})
		Context("search document", func() {
			It("search document should be successful", func() {
				docs, err := Chain.Search(context.TODO(), &doc.DocumentQuery{
					AttrQueries: []*doc.AttrQuery{{
						Attr:   "namespace",
						Option: "=",
						Value:  "test-ns",
					}},
					Search: "test",
				}, []*doc.DocumentAttrQuery{})
				Expect(err).Should(BeNil())
				Expect(docs).Should(HaveLen(3))
			})
			It("search document with attr should be successful", func() {
				docs, err := Chain.Search(context.TODO(), &doc.DocumentQuery{
					AttrQueries: []*doc.AttrQuery{{
						Attr:   "namespace",
						Option: "=",
						Value:  "test-ns",
					}},
					Search: "test",
				}, []*doc.DocumentAttrQuery{
					{
						AttrQueries: []*doc.AttrQuery{
							{
								Attr:   "parentId",
								Option: "=",
								Value:  parentId1,
							},
							{
								Attr:   "namespace",
								Option: "=",
								Value:  "test-ns",
							},
						},
					},
				})
				Expect(err).Should(BeNil())
				Expect(docs).Should(HaveLen(2))
			})
		})
		Context("plugin should work", func() {
			It("header plugin should work", func() {
				doc3 := &doc.Document{
					Id:        "100",
					Namespace: "test-ns",
					EntryId:   "100",
					Name:      "test-name-100",
					Content:   "<p>test</p><img src=\"http://abc\"/>",
					CreatedAt: 1733584543,
					UpdatedAt: 1733584543,
				}
				err := Chain.Store(context.TODO(), doc3)
				Expect(err).Should(BeNil())
				Expect(doc3.HeaderImage).Should(Equal("http://abc"))
			})
		})
		Context("delete document", func() {
			It("delete document by filter should be successful", func() {
				err := Chain.Store(context.TODO(), &doc.Document{
					Id:        "12",
					Namespace: "test-ns",
					EntryId:   "10",
					Name:      "test-name-10",
				})
				Expect(err).Should(BeNil())
				err = Chain.StoreAttr(context.TODO(), &doc.DocumentAttr{
					Id:        "13",
					Namespace: "test-ns",
					EntryId:   "10",
				})
				Expect(err).Should(BeNil())
				err = Chain.DeleteByFilter(context.TODO(), doc.DocumentAttrQuery{
					AttrQueries: []*doc.AttrQuery{
						{
							Attr:   "namespace",
							Option: "=",
							Value:  "test-ns",
						},
						{
							Attr:   "entryId",
							Option: "=",
							Value:  "10",
						},
					}},
				)
				Expect(err).Should(BeNil())
				docs, err := Chain.Search(context.TODO(), &doc.DocumentQuery{
					AttrQueries: []*doc.AttrQuery{{
						Attr:   "entryId",
						Option: "=",
						Value:  "10",
					}},
					Search: "test",
				}, []*doc.DocumentAttrQuery{})
				Expect(err).Should(BeNil())
				Expect(docs).Should(HaveLen(0))
			})
		})
	})
})
