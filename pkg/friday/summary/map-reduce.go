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

package summary

import (
	"fmt"
	"strings"

	"github.com/basenana/friday/pkg/llm/prompts"
)

func (s *Summary) MapReduce(docs []string) (summary string, err error) {
	// collapse
	newDocs, err := s.splitDocs(s.summaryPrompt, docs)
	if err != nil {
		return "", err
	}
	s.log.Debugf("spilt docs to %d newDocs", len(newDocs))

	splitedSummaries := []string{}
	for _, splitedDocs := range newDocs {
		d, err := s.Stuff(splitedDocs)
		if err != nil {
			return "", err
		}
		splitedSummaries = append(splitedSummaries, d)
	}

	// combine
	return s.combine(splitedSummaries)
}

func (s *Summary) splitDocs(p prompts.PromptTemplate, docs []string) ([][]string, error) {
	collapseDocs := [][]string{}
	subDocs := []string{}

	for _, doc := range docs {
		subDocs = append(subDocs, doc)
		subLength, err := s.getLength(p, subDocs)
		if err != nil {
			return nil, err
		}
		if subLength > s.limitToken {
			if len(subDocs) == 1 {
				return nil, fmt.Errorf("a single part was longer than the context length, can not handle it")
			}
			collapseDocs = append(collapseDocs, subDocs[0:len(subDocs)-1])
			subDocs = subDocs[len(subDocs)-1:]
		}
	}
	collapseDocs = append(collapseDocs, subDocs)
	return collapseDocs, nil
}

func (s *Summary) getLength(p prompts.PromptTemplate, docs []string) (length int, err error) {
	doc := strings.Join(docs, "\n")
	res, err := p.String(map[string]string{"context": doc})
	if err != nil {
		return 0, err
	}
	return len(res), nil
}

func (s *Summary) combine(summaries []string) (summary string, err error) {
	newSummaries, err := s.splitDocs(s.combinePrompt, summaries)
	if err != nil {
		return "", err
	}
	combinedSummaries := []string{}
	for _, subSummaries := range newSummaries {
		subSummary := strings.Join(subSummaries, "\n")
		res, err := s.llm.Chat(s.combinePrompt, map[string]string{"context": subSummary})
		if err != nil {
			return "", err
		}
		combinedSummaries = append(combinedSummaries, res[0])
	}

	if len(combinedSummaries) == 1 {
		return combinedSummaries[0], nil
	}
	return s.combine(combinedSummaries)
}