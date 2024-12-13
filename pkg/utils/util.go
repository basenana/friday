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

package utils

import (
	"reflect"
	"sort"
)

func ToPtr[T any](t T) *T {
	return &t
}

func Equal(a []string, b *[]string) bool {
	if b == nil {
		return false
	}
	aa := deDup(a)
	bb := deDup(*b)
	sort.Sort(sort.StringSlice(aa))
	sort.Sort(sort.StringSlice(bb))
	return reflect.DeepEqual(aa, bb)
}

func deDup(res []string) []string {
	uniqueMap := make(map[string]bool)
	result := []string{}

	for _, str := range res {
		if _, exists := uniqueMap[str]; !exists {
			uniqueMap[str] = true
			result = append(result, str)
		}
	}
	return result
}
