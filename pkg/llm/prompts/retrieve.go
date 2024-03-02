/*
 * Copyright 2023 friday
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

package prompts

import (
	"bytes"
	"html/template"
)

type RetrievePrompt struct {
	template    string
	instruction string
}

const RetrieveTemplate = `Given an instruction, please make a judgment on whether finding some external documents from the web (e.g., Wikipedia) helps to generate a better response. Please answer [Yes] or [No] and write an explanation.

##
Instruction: Give three tips for staying healthy.
Need retrieval?: [Yes]
Explanation: There might be some online sources listing three tips for staying healthy or some reliable sources to explain the effects of different behaviors on health. So retrieving documents is helpful to improve the response to this query.

##
Instruction: Describe a time when you had to make a difficult decision.
Need retrieval?: [No]
Explanation: This instruction is asking about some personal experience and thus it does not require one to find some external documents.

##
Instruction: Write a short story in third person narration about a protagonist who has to make an important career decision.
Need retrieval?: [No]
Explanation: This instruction asks us to write a short story, which does not require external evidence to verify.

##
Instruction: What is the capital of France?
Need retrieval?: [Yes]
Explanation: While the instruction simply asks us to answer the capital of France, which is a widely known fact, retrieving web documents for this question can still help.

## 
Instruction: Find the area of a circle given its radius. Radius = 4
Need retrieval?: [No]
Explanation: This is a math question and although we may be able to find some documents describing a formula, it is unlikely to find a document exactly mentioning the answer.

##
Instruction: Arrange the words in the given sentence to form a grammatically correct sentence. quickly the brown fox jumped
Need retrieval?: [No]
Explanation: This task doesn't require any external evidence, as it is a simple grammatical question.

##
Instruction: Explain the process of cellular respiration in plants.
Need retrieval?: [Yes]
Explanation: This instruction asks for a detailed description of a scientific concept, and is highly likely that we can find a reliable and useful document to support the response.

##
Instruction: {{ .Instruction }}
Need retrieval?: `

var _ PromptTemplate = &RetrievePrompt{}

func NewRetrievePrompt(t string) PromptTemplate {
	if t == "" {
		t = RetrieveTemplate
	}
	return &RetrievePrompt{template: t}
}

func (p *RetrievePrompt) String(promptContext map[string]string) (string, error) {
	p.instruction = promptContext["instruction"]
	temp := template.Must(template.New("retrieve").Parse(p.template))
	prompt := new(bytes.Buffer)
	err := temp.Execute(prompt, p)
	if err != nil {
		return "", err
	}
	return prompt.String(), nil
}
