/*
 * Copyright 2023 Friday Author.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package spliter

import (
	"reflect"
	"testing"

	"github.com/basenana/friday/pkg/models/vector"
	"github.com/basenana/friday/pkg/utils/logger"
)

func TestTextSpliter_Merge(t1 *testing.T) {
	type fields struct {
		separator    string
		chunkSize    int
		chunkOverlap int
	}
	type args struct {
		elements []vector.Element
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []vector.Element
	}{
		{
			name: "test1",
			fields: fields{
				separator:    "\n",
				chunkSize:    50,
				chunkOverlap: 2,
			},
			args: args{
				elements: []vector.Element{
					{
						ID:       "123",
						Name:     "test",
						Group:    0,
						OID:      0,
						ParentId: 0,
						Content:  "this is a test",
					},
					{
						Content: "hello world",
						Name:    "test",
						Group:   1,
					},
				},
			},
			want: []vector.Element{
				{
					Content: "this is a test\nhello world",
					Name:    "test",
					Group:   0,
				},
			},
		},
		{
			name: "test2",
			fields: fields{
				separator:    "\n",
				chunkSize:    50,
				chunkOverlap: 2,
			},
			args: args{
				elements: []vector.Element{
					{
						Content: "this is a test",
						Name:    "test",
						Group:   0,
					},
					{
						Content: "hello world",
						Name:    "test",
						Group:   1,
					},
					{
						Content: "你好",
						Name:    "hello",
						Group:   0,
					},
				},
			},
			want: []vector.Element{
				{
					Content: "this is a test\nhello world",
					Name:    "test",
					Group:   0,
				},
				{
					Content: "你好",
					Name:    "hello",
					Group:   0,
				},
			},
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			t := &TextSpliter{
				log:          logger.NewLogger("test"),
				separator:    tt.fields.separator,
				chunkSize:    tt.fields.chunkSize,
				chunkOverlap: tt.fields.chunkOverlap,
			}
			got := t.Merge(tt.args.elements)
			if len(got) != len(tt.want) {
				t1.Errorf("Merge() = %v, want %v", got, tt.want)
			}
			for _, g := range got {
				gotInWant := false
				for _, w := range tt.want {
					if reflect.DeepEqual(g, w) {
						gotInWant = true
					}
				}
				if !gotInWant {
					t1.Errorf("Merge() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestTextSpliter_Split(t1 *testing.T) {
	type fields struct {
		separator    string
		chunkSize    int
		chunkOverlap int
	}
	type args struct {
		text string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []string
	}{
		{
			name: "test1",
			fields: fields{
				separator:    "\n",
				chunkSize:    3,
				chunkOverlap: 2,
			},
			args: args{
				text: "hello world\nthis is a test\n",
			},
			want: []string{"hello world", "this is a test"},
		},
		{
			name: "test2",
			fields: fields{
				separator:    "\n",
				chunkSize:    7,
				chunkOverlap: 2,
			},
			args: args{
				text: "hello world\nthis is a test\n",
			},
			want: []string{"hello world\nthis is a test"},
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			t := &TextSpliter{
				log:          logger.NewLogger("test"),
				separator:    tt.fields.separator,
				chunkSize:    tt.fields.chunkSize,
				chunkOverlap: tt.fields.chunkOverlap,
			}
			if got := t.Split(tt.args.text); !reflect.DeepEqual(got, tt.want) {
				t1.Errorf("Split() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTextSpliter_join(t1 *testing.T) {
	type fields struct {
		separator string
	}
	type args struct {
		docs []string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
	}{
		{
			name: "test",
			fields: fields{
				separator: "\n",
			},
			args: args{
				docs: []string{"this is a test", "hello friday"},
			},
			want: `this is a test
hello friday`,
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			t := &TextSpliter{
				log:       logger.NewLogger("test"),
				separator: tt.fields.separator,
			}
			if got := t.join(tt.args.docs); got != tt.want {
				t1.Errorf("join() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTextSpliter_length(t1 *testing.T) {
	type args struct {
		d string
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "test1",
			args: args{
				d: "this is a test",
			},
			want: 4,
		},
		{
			name: "test2",
			args: args{
				d: "",
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			t := &TextSpliter{
				log: logger.NewLogger("test"),
			}
			if got := t.length(tt.args.d); got != tt.want {
				t1.Errorf("length() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTextSpliter_merge(t1 *testing.T) {
	type fields struct {
		separator    string
		chunkSize    int
		chunkOverlap int
	}
	type args struct {
		splits []string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []string
	}{
		{
			name: "test1",
			fields: fields{
				separator:    "\n",
				chunkSize:    5,
				chunkOverlap: 2,
			},
			args: args{
				splits: []string{
					"this is a test",
					"hello world",
				},
			},
			want: []string{"this is a test", "hello world"},
		},
		{
			name: "test2",
			fields: fields{
				separator:    "\n",
				chunkSize:    5,
				chunkOverlap: 2,
			},
			args: args{
				splits: []string{
					"yeah",
					"hey",
					"this is a test",
					"hello world",
				},
			},
			want: []string{"yeah\nhey", "hey\nthis is a test", "hello world"},
		},
		{
			name: "test3",
			fields: fields{
				separator:    "\n",
				chunkSize:    5,
				chunkOverlap: 4,
			},
			args: args{
				splits: []string{
					"yeah",
					"hey",
					"hello",
					"this is a test",
					"hello world",
				},
			},
			want: []string{"yeah\nhey\nhello", "hello\nthis is a test", "hello world"},
		},
		{
			name: "test4",
			fields: fields{
				separator:    "\n",
				chunkSize:    5,
				chunkOverlap: 4,
			},
			args: args{
				splits: []string{
					"yeah",
					"",
					"hey",
					"hello",
					"good morning",
					"how are you",
					"this is a test",
					"hello world",
				},
			},
			want: []string{"yeah\nhey\nhello\ngood morning", "good morning\nhow are you", "this is a test", "hello world"},
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			t := &TextSpliter{
				log:          logger.NewLogger("test"),
				separator:    tt.fields.separator,
				chunkSize:    tt.fields.chunkSize,
				chunkOverlap: tt.fields.chunkOverlap,
			}
			if got := t.merge(tt.args.splits); !reflect.DeepEqual(got, tt.want) {
				t1.Errorf("merge() = %v, want %v", got, tt.want)
			}
		})
	}
}
