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

package plugin

import (
	"context"
	"testing"

	"github.com/basenana/friday/pkg/models/doc"
)

func TestHeaderImgPlugin_Run(t *testing.T) {
	tests := []struct {
		name          string
		document      *doc.Document
		wantErr       bool
		wantHeaderImg string
	}{
		{
			name: "test-relative-address",
			document: &doc.Document{
				WebUrl:  "https://blog.abc/123",
				Content: "<p><img src=\"media/123.png\" alt=\"\" /></p>",
			},
			wantErr:       false,
			wantHeaderImg: "https://blog.abc/media/123.png",
		},
		{
			name: "test-normal",
			document: &doc.Document{
				WebUrl:  "https://blog.abc",
				Content: "<p><img src=\"https://def/123.png\" /></p>",
			},
			wantErr:       false,
			wantHeaderImg: "https://def/123.png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HeaderImgPlugin{}
			if err := h.Run(context.TODO(), tt.document); (err != nil) != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.document.HeaderImage != tt.wantHeaderImg {
				t.Errorf("Run() got = %v, want %v", tt.document.HeaderImage, tt.wantHeaderImg)
			}
		})
	}
}
