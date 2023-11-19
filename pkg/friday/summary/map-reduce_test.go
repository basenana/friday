/*
 Copyright 2023 Friday Author.

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

package summary

import (
	"testing"

	"github.com/basenana/friday/pkg/llm/prompts"
	"github.com/basenana/friday/pkg/utils/logger"
)

func TestSummary_getLength(t *testing.T) {
	type fields struct {
		limitToken int
	}
	type args struct {
		docs []string
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		wantLength int
		wantErr    bool
	}{
		{
			name: "test1",
			fields: fields{
				limitToken: 10,
			},
			args: args{
				docs: []string{
					"I am a doc",
					"You are a doc too",
				},
			},
			wantLength: 23,
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Summary{
				log:           logger.NewLogger("test"),
				summaryPrompt: prompts.NewSummaryPrompt(),
				combinePrompt: prompts.NewCombinePrompt(),
				limitToken:    tt.fields.limitToken,
			}
			gotLength, err := s.getLength(s.summaryPrompt, tt.args.docs)
			if (err != nil) != tt.wantErr {
				t.Errorf("getLength() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotLength != tt.wantLength {
				t.Errorf("getLength() gotLength = %v, want %v", gotLength, tt.wantLength)
			}
		})
	}
}
