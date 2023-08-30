package friday

import (
	"fmt"
	"strings"

	"friday/pkg/llm/prompts"
)

func (f *Friday) Question(prompt prompts.PromptTemplate, q string) (string, error) {
	c, err := f.searchDocs(q)
	if err != nil {
		return "", err
	}
	if f.llm != nil {
		ans, err := f.llm.Completion(prompt, map[string]string{
			"context":  c,
			"question": q,
		})
		if err != nil {
			return "", fmt.Errorf("llm completion error: %w", err)
		}
		return ans[0], nil
	}
	return c, nil
}

func (f *Friday) searchDocs(q string) (string, error) {
	f.log.Debugf("vector query for %s ...", q)
	qv, _, err := f.embedding.VectorQuery(q)
	if err != nil {
		return "", fmt.Errorf("vector embedding error: %w", err)
	}
	contexts, err := f.vector.Search(qv, defaultTopK)
	if err != nil {
		return "", fmt.Errorf("vector search error: %w", err)
	}

	cs := []string{}
	for _, c := range contexts {
		f.log.Debugf("searched from [%s] for %s", c.Metadata["source"], c.Content)
		cs = append(cs, c.Content)
	}
	return strings.Join(cs, "\n"), nil
}
