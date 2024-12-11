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
	"fmt"
)

var (
	DocFilterableAttrs     = []string{"namespace", "id", "entryId", "kind", "name", "source", "webUrl", "createdAt", "updatedAt"}
	DocAttrFilterableAttrs = []string{"namespace", "entryId", "key", "id", "kind", "value"}
	DocSortAttrs           = []string{"createdAt", "updatedAt", "name"}
)

type DocPtrInterface interface {
	ID() string
	EntryID() string
	Type() string
	String() string
}

type Document struct {
	Id        string `json:"id"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	EntryId   string `json:"entryId"`
	Name      string `json:"name"`
	Source    string `json:"source,omitempty"`
	WebUrl    string `json:"webUrl,omitempty"`

	Content     string `json:"content"`
	Summary     string `json:"summary,omitempty"`
	HeaderImage string `json:"headerImage,omitempty"`
	SubContent  string `json:"subContent,omitempty"`

	CreatedAt int64 `json:"createdAt,omitempty"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

func (d *Document) ID() string {
	return d.Id
}

func (d *Document) EntryID() string {
	return d.EntryId
}

func (d *Document) Type() string {
	return "document"
}

func (d *Document) String() string {
	return fmt.Sprintf("EntryId(%s) %s", d.EntryId, d.Name)
}

type DocumentList []*Document

func (d DocumentList) String() string {
	result := ""
	for _, doc := range d {
		result += fmt.Sprintf("EntryId(%s) %s\n", doc.EntryId, doc.Name)
	}
	return result
}

var _ DocPtrInterface = &Document{}
