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

package files

import "testing"

func TestLength(t *testing.T) {
	type args struct {
		doc string
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "test1",
			args: args{
				doc: "I am a doc.",
			},
			want: 5,
		},
		{
			name: "test2",
			args: args{
				doc: "一个用户态的文件系统包含一个内核模块和一个用户空间 daemon 进程。内核模块加载时被注册成 Linux 虚拟文件系统的一个 fuse 文件系统驱动。",
			},
			want: 58,
		},
		{
			name: "test3",
			args: args{
				doc: `profile app flags=(attach_disconnected,mediate_deleted) {
  #include &amp;lt;abstractions/base&amp;gt;
  mount,
  umount,
  capability sys_admin, 
  ...
}
`,
			},
			want: 36,
		},
		{
			name: "test4",
			args: args{""},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Length(tt.args.doc); got != tt.want {
				t.Errorf("Length() = %v, want %v", got, tt.want)
			}
		})
	}
}
