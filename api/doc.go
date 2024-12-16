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
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/basenana/friday/pkg/models"
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
		enId, _ := strconv.Atoi(entryId)
		body.EntryId = int64(enId)
		if err := body.Valid(); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		// store the document
		if err := s.chain.CreateDocument(c, &body.Document); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				return
			}
			c.String(500, fmt.Sprintf("store document error: %s", err))
			return
		}
		c.JSON(200, body.Document)
	}
}

func (s *HttpServer) update() gin.HandlerFunc {
	return func(c *gin.Context) {
		entryId := c.Param("entryId")
		namespace := c.Param("namespace")
		body := &DocUpdateRequest{}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		body.Namespace = namespace
		enId, _ := strconv.Atoi(entryId)
		body.EntryId = int64(enId)
		// update the document
		if err := s.chain.UpdateDocument(c, body.ToModel()); err != nil {
			c.String(500, fmt.Sprintf("update document error: %s", err))
			return
		}
		c.JSON(200, body)
	}
}

func (s *HttpServer) get() gin.HandlerFunc {
	return func(c *gin.Context) {
		namespace := c.Param("namespace")
		entryId := c.Param("entryId")
		enId, _ := strconv.Atoi(entryId)
		document, err := s.chain.GetDocument(c, namespace, int64(enId))
		if err != nil {
			if err == models.ErrNotFound {
				c.String(404, fmt.Sprintf("document not found: %s", entryId))
				return
			}
			c.String(500, fmt.Sprintf("get document error: %s", err))
			return
		}
		if document == nil {
			c.String(404, fmt.Sprintf("document not found: %s", entryId))
			return
		}
		c.JSON(200, document)
	}
}

func (s *HttpServer) filter() gin.HandlerFunc {
	return func(c *gin.Context) {
		docQuery := getFilterQuery(c)
		if docQuery == nil {
			return
		}

		ctx := models.WithPagination(c, models.NewPagination(docQuery.Page, docQuery.PageSize))

		docs, err := s.chain.Search(ctx, docQuery)
		if err != nil {
			c.String(500, fmt.Sprintf("search document error: %s", err))
			return
		}

		c.JSON(200, docs)
	}
}

func getFilterQuery(c *gin.Context) *doc.DocumentFilter {
	namespace := c.Param("namespace")
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		c.String(400, fmt.Sprintf("invalid page number: %s", c.Query("page")))
		return nil
	}
	pageSize, err := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	if err != nil {
		c.String(400, fmt.Sprintf("invalid pagesize: %s", c.Query("page")))
		return nil
	}

	sort, err := strconv.Atoi(c.DefaultQuery("sort", "4"))
	if err != nil {
		c.String(400, fmt.Sprintf("invalid sort: %s", c.Query("page")))
		return nil
	}
	docQuery := &doc.DocumentFilter{
		Namespace: namespace,
		Search:    c.Query("search"),
		FuzzyName: c.Query("fuzzyName"),
		Source:    c.Query("source"),
		Page:      int64(page),
		PageSize:  int64(pageSize),
		Order: doc.DocumentOrder{
			Order: doc.DocOrder(sort),
			Desc:  c.Query("desc") == "true",
		},
	}

	if c.Query("mark") != "" {
		docQuery.Marked = utils.ToPtr(c.Query("mark") == "true")
	}
	if c.Query("unRead") != "" {
		docQuery.Unread = utils.ToPtr(c.Query("unRead") == "true")
	}
	parentId := c.Query("parentId")
	if parentId != "" {
		pId, err := strconv.Atoi(c.Query("parentId"))
		if err != nil {
			c.String(400, fmt.Sprintf("invalid parentId: %s", c.Query("page")))
			return nil
		}
		docQuery.ParentID = utils.ToPtr(int64(pId))
	}
	createAtStart := c.Query("createAtStart")
	if createAtStart != "" {
		createAtStartTimestamp, err := strconv.Atoi(createAtStart)
		if err != nil {
			c.String(400, fmt.Sprintf("invalid createAtStart: %s", c.Query("page")))
			return nil
		}
		docQuery.CreatedAtStart = utils.ToPtr(time.Unix(int64(createAtStartTimestamp), 0))
	}
	createAtEnd := c.Query("createAtEnd")
	if createAtEnd != "" {
		createAtEndTimestamp, err := strconv.Atoi(createAtEnd)
		if err != nil {
			c.String(400, fmt.Sprintf("invalid createAtEnd: %s", c.Query("page")))
			return nil
		}
		docQuery.ChangedAtEnd = utils.ToPtr(time.Unix(int64(createAtEndTimestamp), 0))
	}
	updatedAtStart := c.Query("updatedAtStart")
	if updatedAtStart != "" {
		updatedAtStartTimestamp, err := strconv.Atoi(updatedAtStart)
		if err != nil {
			c.String(400, fmt.Sprintf("invalid updatedAtStart: %s", c.Query("page")))
			return nil
		}
		docQuery.ChangedAtStart = utils.ToPtr(time.Unix(int64(updatedAtStartTimestamp), 0))
	}
	updatedAtEnd := c.Query("updatedAtEnd")
	if updatedAtEnd != "" {
		updatedAtEndTimestamp, err := strconv.Atoi(updatedAtEnd)
		if err != nil {
			c.String(400, fmt.Sprintf("invalid updatedAtEnd: %s", c.Query("page")))
			return nil
		}
		docQuery.ChangedAtEnd = utils.ToPtr(time.Unix(int64(updatedAtEndTimestamp), 0))
	}
	return docQuery
}

func (s *HttpServer) delete() gin.HandlerFunc {
	return func(c *gin.Context) {
		namespace := c.Param("namespace")
		entryId := c.Param("entryId")

		enId, _ := strconv.Atoi(entryId)
		if err := s.chain.Delete(c, namespace, int64(enId)); err != nil {
			c.String(500, fmt.Sprintf("delete document error: %s", err))
			return
		}
		c.JSON(200, gin.H{"entryId": entryId})
	}
}
