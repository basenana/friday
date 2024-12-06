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

func (s *HttpServer) search() gin.HandlerFunc {
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
		sort := c.DefaultQuery("sort", "createdAt")
		desc := c.DefaultQuery("desc", "true") == "true"
		var (
			unread *bool
			mark   *bool
		)
		if c.Query("unread") != "" {
			b := c.Query("unread") == "true"
			unread = &b
		}
		if c.Query("mark") != "" {
			b := c.Query("mark") == "true"
			mark = &b
		}
		docQuery := DocQuery{
			Namespace:   namespace,
			Source:      c.Query("source"),
			WebUrl:      c.Query("webUrl"),
			ParentID:    c.Query("parentID"),
			UnRead:      unread,
			Mark:        mark,
			Search:      c.Query("search"),
			HitsPerPage: int64(pageSize),
			Page:        int64(page),
			Sort:        sort,
			Desc:        desc,
		}
		docs, err := s.chain.Search(c, docQuery.ToQuery(), docQuery.GetAttrQueries())
		if err != nil {
			c.String(500, fmt.Sprintf("search document error: %s", err))
			return
		}
		c.JSON(200, docs)
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
