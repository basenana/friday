package prompts

import (
	"bytes"
	"html/template"
)

type WeChatConclusionPrompt struct {
	template string
	Context  string
}

const WeChatConclusionTemplate = `请根据以下每天发生的事情，总结出这一段时间里发生的事情。

聊天记录：
{{ .Context }}
`

var _ PromptTemplate = &WeChatConclusionPrompt{}

func NewWeChatConclusionPrompt() PromptTemplate {
	return &WeChatConclusionPrompt{template: WeChatConclusionTemplate}
}

func (w *WeChatConclusionPrompt) String(promptContext map[string]string) (string, error) {
	w.Context = promptContext["context"]
	temp := template.Must(template.New("knowledge").Parse(w.template))
	prompt := new(bytes.Buffer)
	err := temp.Execute(prompt, w)
	if err != nil {
		return "", err
	}
	return prompt.String(), nil
}
