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

package friday

import (
	"context"

	"github.com/basenana/friday/pkg/friday/summary"
	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/utils/files"
)

func (f *Friday) Summary(ctx context.Context, elements []models.Element, summaryType summary.SummaryType) (map[string]string, map[string]int, error) {
	result := make(map[string]string)
	s := summary.NewSummary(f.Log, f.LLM, f.LimitToken, f.Prompts)

	docs := make(map[string][]string)
	for _, element := range elements {
		if _, ok := docs[element.Name]; !ok {
			docs[element.Name] = []string{element.Content}
		} else {
			docs[element.Name] = append(docs[element.Name], element.Content)
		}
	}
	totalUsage := make(map[string]int)
	for source, doc := range docs {
		summaryOfFile, usage, err := s.Summary(ctx, doc, summaryType)
		if err != nil {
			return nil, nil, err
		}
		result[source] = summaryOfFile
		for k, v := range usage {
			totalUsage[k] = totalUsage[k] + v
		}
	}
	f.Log.Debugf("Summary result: %s", result)
	return result, totalUsage, nil
}

func (f *Friday) SummaryFromFile(ctx context.Context, file models.File, summaryType summary.SummaryType) (map[string]string, map[string]int, error) {
	s := summary.NewSummary(f.Log, f.LLM, f.LimitToken, f.Prompts)
	// split doc
	docs := f.Spliter.Split(file.Content)
	// summary
	summaryOfFile, usage, err := s.Summary(ctx, docs, summaryType)
	if err != nil {
		return nil, nil, err
	}
	return map[string]string{file.Name: summaryOfFile}, usage, err
}

func (f *Friday) SummaryFromOriginFile(ctx context.Context, ps string, summaryType summary.SummaryType) (map[string]string, map[string]int, error) {
	s := summary.NewSummary(f.Log, f.LLM, f.LimitToken, f.Prompts)
	fs, err := files.ReadFiles(ps)
	if err != nil {
		return nil, nil, err
	}

	result := make(map[string]string)
	totalUsage := make(map[string]int)
	for name, file := range fs {
		// split doc
		subDocs := f.Spliter.Split(file)
		summaryOfFile, usage, err := s.Summary(ctx, subDocs, summaryType)
		if err != nil {
			return nil, nil, err
		}
		result[name] = summaryOfFile
		for k, v := range usage {
			totalUsage[k] = totalUsage[k] + v
		}
	}

	return result, totalUsage, nil
}
