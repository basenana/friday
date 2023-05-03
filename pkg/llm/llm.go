package llm

import "friday/pkg/llm/prompts"

type LLM interface {
	Completion(prompt prompts.PromptTemplate, parameters map[string]string) ([]string, error)
}
