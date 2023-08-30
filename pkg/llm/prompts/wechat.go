package prompts

import (
	"bytes"
	"html/template"
)

type WeChatPrompt struct {
	template string
	Context  string
}

const WeChatTemplate = `以下是一段聊天记录。请把这些聊天记录以聊天内容相近的合并成一个主题，并以主题为单位，整理成概要。注意请不要写成流水账。

返回的概要格式：
主题1：xxx
概要1：xxxxxx
===
主题2：xxx
概要2：xxxxxx

聊天记录：
{{ .Context }}
`

var _ PromptTemplate = &WeChatPrompt{}

func NewWeChatPrompt() PromptTemplate {
	return &WeChatPrompt{template: WeChatTemplate}
}

func (w *WeChatPrompt) String(promptContext map[string]string) (string, error) {
	w.Context = promptContext["context"]
	temp := template.Must(template.New("knowledge").Parse(w.template))
	prompt := new(bytes.Buffer)
	err := temp.Execute(prompt, w)
	if err != nil {
		return "", err
	}
	return prompt.String(), nil
}
