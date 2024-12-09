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

package doc

import (
	"encoding/json"
	"fmt"

	"github.com/meilisearch/meilisearch-go"
)

type DocumentQuery struct {
	AttrQueries []*AttrQuery

	Search      string
	HitsPerPage int64
	Page        int64
	Offset      int64
	Limit       int64
	Sort        []Sort
}

type Sort struct {
	Attr string
	Asc  bool
}

func (s *Sort) String() string {
	if s.Asc {
		return fmt.Sprintf("%s:asc", s.Attr)
	}
	return fmt.Sprintf("%s:desc", s.Attr)
}

type DocumentAttrQuery struct {
	AttrQueries []*AttrQuery
}

func (q *DocumentAttrQuery) String() string {
	result := ""
	for _, aq := range q.AttrQueries {
		result += aq.String() + " "
	}
	return result
}

type AttrQuery struct {
	Attr   string
	Option string
	Value  interface{}
}

func (aq *AttrQuery) ToFilter() interface{} {
	vs, _ := json.Marshal(aq.Value)
	return fmt.Sprintf("%s %s %s", aq.Attr, aq.Option, vs)
}

func (aq *AttrQuery) String() string {
	return aq.ToFilter().(string)
}

func (q *DocumentQuery) String() string {
	filters := ""
	for _, aq := range q.AttrQueries {
		filters += aq.String() + " "
	}
	return fmt.Sprintf("search: [%s], attr query: [%s]", q.Search, filters)
}

func (q *DocumentQuery) ToRequest() *meilisearch.SearchRequest {
	// build filter
	filter := []interface{}{}
	for _, aq := range q.AttrQueries {
		filter = append(filter, aq.ToFilter())
	}
	sorts := []string{}
	for _, s := range q.Sort {
		sorts = append(sorts, s.String())
	}

	return &meilisearch.SearchRequest{
		Offset:      q.Offset,
		Limit:       q.Limit,
		Sort:        sorts,
		HitsPerPage: q.HitsPerPage,
		Page:        q.Page,
		Query:       q.Search,
		Filter:      filter,
	}
}

func (q *DocumentAttrQuery) ToRequest() *meilisearch.SearchRequest {
	filter := []interface{}{}
	for _, aq := range q.AttrQueries {
		filter = append(filter, aq.ToFilter())
	}
	return &meilisearch.SearchRequest{
		Filter:      filter,
		Limit:       10000,
		HitsPerPage: 10000,
		Query:       "",
	}
}
