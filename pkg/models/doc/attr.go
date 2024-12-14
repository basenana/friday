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

import "fmt"

type DocumentAttr struct {
	Id        string      `json:"id"`
	Kind      string      `json:"kind"`
	Namespace string      `json:"namespace"`
	EntryId   string      `json:"entryId"`
	Key       string      `json:"key"`
	Value     interface{} `json:"value"`
}

var (
	DocAttrFilterableAttrs = []string{"namespace", "entryId", "key", "id", "kind", "value"}
	DocAttrSortAttrs       = []string{"createdAt", "updatedAt"}
)

var _ DocPtrInterface = &DocumentAttr{}

func (d *DocumentAttr) ID() string {
	return d.Id
}

func (d *DocumentAttr) EntryID() string {
	return d.EntryId
}

func (d *DocumentAttr) Type() string {
	return "attr"
}

func (d *DocumentAttr) String() string {
	return fmt.Sprintf("EntryId(%s) %s: %v", d.EntryId, d.Key, d.Value)
}

type DocumentAttrList []*DocumentAttr

func (d DocumentAttrList) String() string {
	result := ""
	for _, attr := range d {
		result += fmt.Sprintf("EntryId(%s) %s: %v\n", attr.EntryId, attr.Key, attr.Value)
	}
	return result
}
