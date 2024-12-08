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

package api

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/basenana/friday/pkg/models/doc"
	"github.com/basenana/friday/pkg/utils"
)

func (s *HttpServer) store() gin.HandlerFunc {
	return func(c *gin.Context) {
		entryId := c.Param("entryId")
		namespace := c.Param("namespace")
		body := &DocRequest{}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		body.Namespace = namespace
		body.EntryId = entryId
		// store the document
		doc := body.ToDocument()
		if err := s.chain.Store(c, doc); err != nil {
			c.String(500, fmt.Sprintf("store document error: %s", err))
			return
		}
		c.JSON(200, doc)
	}
}

func (s *HttpServer) update() gin.HandlerFunc {
	return func(c *gin.Context) {
		entryId := c.Param("entryId")
		namespace := c.Param("namespace")
		body := &DocAttrRequest{}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		body.Namespace = namespace
		body.EntryId = entryId
		// update the document
		attrs := body.ToDocAttr()
		for _, attr := range attrs {
			if err := s.chain.StoreAttr(c, attr); err != nil {
				c.String(500, fmt.Sprintf("update document error: %s", err))
				return
			}
		}
		c.JSON(200, body)
	}
}

func (s *HttpServer) get() gin.HandlerFunc {
	return func(c *gin.Context) {
		namespace := c.Param("namespace")
		entryId := c.Param("entryId")
		document, err := s.chain.GetDocument(c, namespace, entryId)
		if err != nil {
			c.String(500, fmt.Sprintf("get document error: %s", err))
			return
		}
		docWithAttr := &DocumentWithAttr{
			Document: document,
		}

		attrs, err := s.chain.GetDocumentAttrs(c, namespace, entryId)
		if err != nil {
			c.String(500, fmt.Sprintf("get document attrs error: %s", err))
			return
		}
		for _, attr := range attrs {
			if attr.Key == "parentId" {
				docWithAttr.ParentID = attr.Value.(string)
			}
			if attr.Key == "mark" {
				marked := attr.Value.(bool)
				docWithAttr.Mark = &marked
			}
			if attr.Key == "unRead" {
				unRead := attr.Value.(bool)
				docWithAttr.UnRead = &unRead
			}
		}

		c.JSON(200, attrs)
	}
}

func (s *HttpServer) filter() gin.HandlerFunc {
	return func(c *gin.Context) {
		namespace := c.Param("namespace")
		page, err := strconv.Atoi(c.DefaultQuery("page", "0"))
		if err != nil {
			c.String(400, fmt.Sprintf("invalid page number: %s", c.Query("page")))
			return
		}
		pageSize, err := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
		if err != nil {
			c.String(400, fmt.Sprintf("invalid pagesize: %s", c.Query("page")))
			return
		}
		docQuery := DocQuery{
			Namespace:   namespace,
			Source:      c.Query("source"),
			WebUrl:      c.Query("webUrl"),
			ParentID:    c.Query("parentID"),
			Search:      c.Query("search"),
			HitsPerPage: int64(pageSize),
			Page:        int64(page),
			Sort:        c.DefaultQuery("sort", "createdAt"),
			Desc:        c.DefaultQuery("desc", "false") == "true",
		}
		createAtStart := c.Query("createAtStart")
		if createAtStart != "" {
			createAtStartTimestamp, err := strconv.Atoi(createAtStart)
			if err != nil {
				c.String(400, fmt.Sprintf("invalid createAtStart: %s", c.Query("page")))
				return
			}
			docQuery.CreatedAtStart = utils.ToPtr(int64(createAtStartTimestamp))
		}
		createAtEnd := c.Query("createAtEnd")
		if createAtEnd != "" {
			createAtEndTimestamp, err := strconv.Atoi(createAtEnd)
			if err != nil {
				c.String(400, fmt.Sprintf("invalid createAtEnd: %s", c.Query("page")))
				return
			}
			docQuery.ChangedAtEnd = utils.ToPtr(int64(createAtEndTimestamp))
		}
		updatedAtStart := c.Query("updatedAtStart")
		if updatedAtStart != "" {
			updatedAtStartTimestamp, err := strconv.Atoi(updatedAtStart)
			if err != nil {
				c.String(400, fmt.Sprintf("invalid updatedAtStart: %s", c.Query("page")))
				return
			}
			docQuery.ChangedAtStart = utils.ToPtr(int64(updatedAtStartTimestamp))
		}
		updatedAtEnd := c.Query("updatedAtEnd")
		if updatedAtEnd != "" {
			updatedAtEndTimestamp, err := strconv.Atoi(updatedAtEnd)
			if err != nil {
				c.String(400, fmt.Sprintf("invalid updatedAtEnd: %s", c.Query("page")))
				return
			}
			docQuery.ChangedAtEnd = utils.ToPtr(int64(updatedAtEndTimestamp))
		}
		fuzzyName := c.Query("fuzzyName")
		if fuzzyName != "" {
			docQuery.FuzzyName = &fuzzyName
		}
		if c.Query("unread") != "" {
			docQuery.UnRead = utils.ToPtr(c.Query("unread") == "true")
		}
		if c.Query("mark") != "" {
			docQuery.Mark = utils.ToPtr(c.Query("mark") == "true")
		}
		docs, err := s.chain.Search(c, docQuery.ToQuery(), docQuery.GetAttrQueries())
		if err != nil {
			c.String(500, fmt.Sprintf("search document error: %s", err))
			return
		}
		ids := []string{}
		for _, doc := range docs {
			ids = append(ids, doc.EntryId)
		}

		allAttrs, err := s.chain.ListDocumentAttrs(c, namespace, ids)
		if err != nil {
			c.String(500, fmt.Sprintf("list document attrs error: %s", err))
			return
		}
		attrsMap := map[string][]doc.DocumentAttr{}
		for _, attr := range allAttrs {
			if attrsMap[attr.EntryId] == nil {
				attrsMap[attr.EntryId] = []doc.DocumentAttr{}
			}
			attrsMap[attr.EntryId] = append(attrsMap[attr.EntryId], attr)
		}

		var docWithAttrs []DocumentWithAttr
		for _, document := range docs {
			docWithAttr := DocumentWithAttr{Document: document}
			attrs := attrsMap[document.EntryId]
			for _, attr := range attrs {
				if attr.Key == "parentId" {
					docWithAttr.ParentID = attr.Value.(string)
				}
				if attr.Key == "mark" {
					marked := attr.Value.(bool)
					docWithAttr.Mark = &marked
				}
				if attr.Key == "unRead" {
					unRead := attr.Value.(bool)
					docWithAttr.UnRead = &unRead
				}
			}
			docWithAttrs = append(docWithAttrs, docWithAttr)
		}

		c.JSON(200, docWithAttrs)
	}
}

func (s *HttpServer) delete() gin.HandlerFunc {
	return func(c *gin.Context) {
		namespace := c.Param("namespace")
		queries := []doc.AttrQuery{}
		entryId := c.Param("entryId")
		queries = append(queries,
			doc.AttrQuery{
				Attr:   "entryId",
				Option: "=",
				Value:  entryId,
			},
			doc.AttrQuery{
				Attr:   "namespace",
				Option: "=",
				Value:  namespace,
			},
		)
		if err := s.chain.DeleteByFilter(c, queries); err != nil {
			c.String(500, fmt.Sprintf("delete document error: %s", err))
			return
		}
		c.JSON(200, gin.H{"entryId": entryId})
	}
}
