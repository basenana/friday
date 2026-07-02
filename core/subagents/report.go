package subagents

import (
	"bytes"
	"strings"
	"time"

	"github.com/basenana/friday/core/session"
)

type Report struct {
	Task                string   `json:"task"`
	WhatChanged         string   `json:"what_changed,omitempty"`
	Findings            string   `json:"findings,omitempty"`
	FilesExamined       []string `json:"files_examined,omitempty"`
	FilesTouched        []string `json:"files_touched,omitempty"`
	OpenQuestions       []string `json:"open_questions,omitempty"`
	RecommendedNextStep string   `json:"recommended_next_step,omitempty"`
}

func BuildReport(task, content string) Report {
	sections := extractReportSections(content,
		"Task",
		"What Changed",
		"Findings",
		"Files Touched",
		"Open Questions",
		"Recommended Next Step",
	)

	report := Report{
		Task:                strings.TrimSpace(task),
		WhatChanged:         strings.TrimSpace(sections["What Changed"]),
		Findings:            firstNonEmpty(strings.TrimSpace(sections["Findings"]), strings.TrimSpace(content)),
		FilesTouched:        firstNonEmptyList(splitReportList(sections["Files Touched"]), extractFileList(content)),
		OpenQuestions:       splitReportList(sections["Open Questions"]),
		RecommendedNextStep: "Integrate the findings into the main thread and continue from the open items above.",
	}
	if next := strings.TrimSpace(sections["Recommended Next Step"]); next != "" {
		report.RecommendedNextStep = next
	}
	return report
}

func BuildExploreReport(task, content string) Report {
	sections := extractReportSections(content,
		"Task",
		"Findings",
		"Files Examined",
		"Files Touched",
		"Open Questions",
		"Recommended Next Step",
	)

	report := Report{
		Task:                strings.TrimSpace(task),
		Findings:            firstNonEmpty(strings.TrimSpace(sections["Findings"]), strings.TrimSpace(content)),
		FilesExamined:       firstNonEmptyList(splitReportList(firstNonEmpty(sections["Files Examined"], sections["Files Touched"])), extractFileList(content)),
		OpenQuestions:       splitReportList(sections["Open Questions"]),
		RecommendedNextStep: "Use these findings to decide the next main-thread action.",
	}
	if next := strings.TrimSpace(sections["Recommended Next Step"]); next != "" {
		report.RecommendedNextStep = next
	}
	return report
}

func FormatReport(report Report) string {
	buf := &bytes.Buffer{}
	buf.WriteString("<subagent_report>\n")
	buf.WriteString("## Task\n")
	buf.WriteString(strings.TrimSpace(report.Task))
	buf.WriteString("\n\n")

	if report.WhatChanged != "" {
		buf.WriteString("## What Changed\n")
		buf.WriteString(strings.TrimSpace(report.WhatChanged))
		buf.WriteString("\n\n")
	}
	if report.Findings != "" {
		buf.WriteString("## Findings\n")
		buf.WriteString(strings.TrimSpace(report.Findings))
		buf.WriteString("\n\n")
	}
	if len(report.FilesExamined) > 0 {
		buf.WriteString("## Files Examined\n")
		for _, file := range report.FilesExamined {
			buf.WriteString("- ")
			buf.WriteString(strings.TrimSpace(file))
			buf.WriteString("\n")
		}
		buf.WriteString("\n")
	}
	if len(report.FilesTouched) > 0 {
		buf.WriteString("## Files Touched\n")
		for _, file := range report.FilesTouched {
			buf.WriteString("- ")
			buf.WriteString(strings.TrimSpace(file))
			buf.WriteString("\n")
		}
		buf.WriteString("\n")
	}
	if len(report.OpenQuestions) > 0 {
		buf.WriteString("## Open Questions\n")
		for _, question := range report.OpenQuestions {
			buf.WriteString("- ")
			buf.WriteString(strings.TrimSpace(question))
			buf.WriteString("\n")
		}
		buf.WriteString("\n")
	}
	if report.RecommendedNextStep != "" {
		buf.WriteString("## Recommended Next Step\n")
		buf.WriteString(strings.TrimSpace(report.RecommendedNextStep))
		buf.WriteString("\n")
	}
	buf.WriteString("</subagent_report>")
	return strings.TrimSpace(buf.String())
}

func extractReportSections(content string, labels ...string) map[string]string {
	sections := make(map[string][]string, len(labels))
	current := ""

	for _, rawLine := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if label, inline, ok := matchReportSection(rawLine, labels); ok {
			current = label
			if inline != "" {
				sections[label] = append(sections[label], inline)
			}
			continue
		}
		if current == "" {
			continue
		}
		sections[current] = append(sections[current], rawLine)
	}

	result := make(map[string]string, len(labels))
	for _, label := range labels {
		result[label] = strings.TrimSpace(strings.Join(sections[label], "\n"))
	}
	return result
}

func matchReportSection(line string, labels []string) (string, string, bool) {
	clean := trimReportHeading(line)
	if clean == "" {
		return "", "", false
	}

	lower := strings.ToLower(clean)
	for _, label := range labels {
		labelLower := strings.ToLower(label)
		switch {
		case lower == labelLower:
			return label, "", true
		case strings.HasPrefix(lower, labelLower+":"):
			return label, strings.TrimSpace(clean[len(label)+1:]), true
		case strings.HasPrefix(lower, labelLower+" - "):
			return label, strings.TrimSpace(clean[len(label)+3:]), true
		case strings.HasPrefix(lower, labelLower+" ("):
			if idx := strings.Index(clean, "):"); idx >= 0 {
				return label, strings.TrimSpace(clean[idx+2:]), true
			}
			return label, "", true
		}
	}
	return "", "", false
}

func trimReportHeading(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}

	trimmed = strings.TrimLeft(trimmed, "#")
	trimmed = strings.TrimSpace(trimmed)

	switch {
	case strings.HasPrefix(trimmed, "- "):
		trimmed = strings.TrimSpace(trimmed[2:])
	case strings.HasPrefix(trimmed, "* "):
		trimmed = strings.TrimSpace(trimmed[2:])
	}

	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i > 0 && i < len(trimmed) && (trimmed[i] == '.' || trimmed[i] == ')') {
		trimmed = strings.TrimSpace(trimmed[i+1:])
	}

	trimmed = strings.Trim(trimmed, "*_`")
	return strings.TrimSpace(trimmed)
}

func splitReportList(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	var items []string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "- "):
			line = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "* "):
			line = strings.TrimSpace(line[2:])
		}
		items = append(items, line)
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func extractFileList(content string) []string {
	var files []string
	for _, ref := range session.ExtractFileRefs(content, "subagent_report", time.Now()) {
		files = append(files, ref.Path)
	}
	return files
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmptyList(lists ...[]string) []string {
	for _, list := range lists {
		if len(list) > 0 {
			return list
		}
	}
	return nil
}
