package prompts

import "testing"

func TestKnowledgePrompt_String(t *testing.T) {
	type fields struct {
		template string
		context  string
		question string
	}
	tests := []struct {
		name    string
		fields  fields
		want    string
		wantErr bool
	}{
		{
			name: "test",
			fields: fields{
				template: QuestionTemplate,
				context:  "test context",
				question: "what is the question?",
			},
			want: `基于以下已知信息，简洁和专业的来回答用户的问题。
如果无法从中得到答案，请说 "根据已知信息无法回答该问题" 或 "没有提供足够的相关信息"，不允许在答案中添加编造成分，答案请使用中文。

已知内容:
test context

问题:
what is the question?`,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &QuestionPrompt{
				template: tt.fields.template,
				Context:  tt.fields.context,
				Question: tt.fields.question,
			}
			got, err := p.String()
			if (err != nil) != tt.wantErr {
				t.Errorf("String() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("String() got = %v, want %v", got, tt.want)
			}
		})
	}
}
