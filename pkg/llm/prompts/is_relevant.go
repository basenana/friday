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

type IsRelevantPrompt struct {
	template    string
	instruction string
	evidence    string
}

const IsRelevantTemplate = `You'll be provided with an instruction, along with evidence and possibly some preceding sentences. When there are preceding sentences, your focus should be on the sentence that comes after them. Your job is to determine if the evidence is relevant to the initial instruction and the preceding context, and provides useful information to complete the task described in the instruction. If the evidence meets this requirement, respond with [Relevant]; otherwise, generate [Irrelevant].

###
Instruction: Given four answer options, A, B, C, and D, choose the best answer.

Input: Earth rotating causes
A: the cycling of AM and PM
B: the creation of volcanic eruptions
C: the cycling of the tides
D: the creation of gravity

Evidence: Rotation causes the day-night cycle which also creates a corresponding cycle of temperature and humidity creates a corresponding cycle of temperature and humidity. Sea level rises and falls twice a day as the earth rotates.

Rating: [Relevant]
Explanation: The evidence explicitly mentions that the rotation causes a day-night cycle, as described in the answer option A.

###
Instruction: age to run for us house of representatives

Evidence: The Constitution sets three qualifications for service in the U.S. Senate: age (at least thirty years of age); U.S. citizenship (at least nine years); and residency in the state a senator represents at the time of election.

Rating: [Irrelevant]
Explanation: The evidence only discusses the ages to run for the US Senate, not for the House of Representatives.

###
Instruction: {{ .Instruction }}

Evidence: {{ .Evidence }}

Rating: `

var _ PromptTemplate = &IsRelevantPrompt{}

func NewIsRelevantPrompt(t string) PromptTemplate {
	if t == "" {
		t = IsRelevantTemplate
	}
	return &IsRelevantPrompt{template: t}
}

func (p *IsRelevantPrompt) String(promptContext map[string]string) (string, error) {
	p.instruction = promptContext["instruction"]
	p.evidence = promptContext["evidence"]
	temp := template.Must(template.New("isRelevant").Parse(p.template))
	prompt := new(bytes.Buffer)
	err := temp.Execute(prompt, p)
	if err != nil {
		return "", err
	}
	return prompt.String(), nil
}
