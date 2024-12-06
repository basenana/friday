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

package vector

type File struct {
	Name     string `json:"name"`
	OID      int64  `json:"oid"`
	ParentId int64  `json:"parent_id"`
	Content  string `json:"content"`
}

type Element struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Group    int       `json:"group"`
	OID      int64     `json:"oid"`
	ParentId int64     `json:"parent_id"`
	Content  string    `json:"content"`
	Vector   []float32 `json:"vector"`
}

type VectorDocQuery struct {
	ParentId int64
	Oid      int64
}
