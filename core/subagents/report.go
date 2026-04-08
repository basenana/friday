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
	FilesTouched        []string `json:"files_touched,omitempty"`
	OpenQuestions       []string `json:"open_questions,omitempty"`
	RecommendedNextStep string   `json:"recommended_next_step,omitempty"`
}

func BuildReport(task, content string) Report {
	report := Report{
		Task:                strings.TrimSpace(task),
		Findings:            strings.TrimSpace(content),
		RecommendedNextStep: "Integrate the findings into the main thread and continue from the open items above.",
	}

	for _, ref := range session.ExtractFileRefs(content, "subagent_report", time.Now()) {
		report.FilesTouched = append(report.FilesTouched, ref.Path)
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
	if len(report.FilesTouched) > 0 {
		buf.WriteString("## Files Touched\n")
		for _, file := range report.FilesTouched {
			buf.WriteString("- ")
			buf.WriteString(file)
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
