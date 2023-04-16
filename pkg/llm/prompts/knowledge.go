package prompts

import (
	"bytes"
	"html/template"
)

type KnowledgePrompt struct {
	template string
	Context  string
	Question string
}

//const KnowledgeTemplate = `基于以下已知信息，简洁和专业的来回答用户的问题。
//如果无法从中得到答案，请说 "根据已知信息无法回答该问题" 或 "没有提供足够的相关信息"，不允许在答案中添加编造成分，答案请使用中文。
//
//已知内容:
//{{ .Context }}
//
//问题:
//{{ .Question }}`

const KnowledgeTemplate = `Answer user questions concisely and professionally based on the following known information.
If you don't know the answer, just say that you don't know, don't try to make up an answer.

Known content:
{{ .Context }}

Question:
{{ .Question }}`

var _ PromptTemplate = &KnowledgePrompt{}

func NewKnowledgePrompt() PromptTemplate {
	return &KnowledgePrompt{template: KnowledgeTemplate}
}

func (p *KnowledgePrompt) String(promptContext map[string]string) (string, error) {
	p.Context = promptContext["context"]
	p.Question = promptContext["question"]
	temp := template.Must(template.New("knowledge").Parse(p.template))
	prompt := new(bytes.Buffer)
	err := temp.Execute(prompt, p)
	if err != nil {
		return "", err
	}
	return prompt.String(), nil
}
