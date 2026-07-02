package subagents

import (
	"strings"
	"testing"
)

func TestExtractReportSections_MarkdownHeadings(t *testing.T) {
	content := "## Task\nfoo bar\n\n## Findings\ndiscovery A\n\n## Files Touched\n- a.go\n- b.go\n"
	sections := extractReportSections(content, "Task", "Findings", "Files Touched")
	if strings.TrimSpace(sections["Task"]) != "foo bar" {
		t.Fatalf("Task mismatch: %q", sections["Task"])
	}
	if !strings.Contains(sections["Findings"], "discovery A") {
		t.Fatalf("Findings mismatch: %q", sections["Findings"])
	}
	if !strings.Contains(sections["Files Touched"], "a.go") {
		t.Fatalf("Files Touched mismatch: %q", sections["Files Touched"])
	}
}

func TestExtractReportSections_ColonFormat(t *testing.T) {
	content := "Task: do something\nFindings: it works\n"
	sections := extractReportSections(content, "Task", "Findings")
	if sections["Task"] != "do something" {
		t.Fatalf("Task mismatch: %q", sections["Task"])
	}
	if sections["Findings"] != "it works" {
		t.Fatalf("Findings mismatch: %q", sections["Findings"])
	}
}

func TestBuildExploreReport_FillsFilesExamined(t *testing.T) {
	content := "## Task\nread hello.txt\n## Findings\nit says hello\n## Files Examined\n- hello.txt\n"
	report := BuildExploreReport("read hello.txt", content)
	if report.Task != "read hello.txt" {
		t.Fatalf("task mismatch: %q", report.Task)
	}
	if !strings.Contains(report.Findings, "hello") {
		t.Fatalf("findings missing: %q", report.Findings)
	}
	found := false
	for _, f := range report.FilesExamined {
		if strings.Contains(f, "hello.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected hello.txt in FilesExamined, got %#v", report.FilesExamined)
	}
}

func TestBuildReport_FillsWhatChangedAndFilesTouched(t *testing.T) {
	content := "## Task\nchange x\n## What Changed\nrefactored y\n## Findings\nimproved\n## Files Touched\n- x.go\n"
	report := BuildReport("change x", content)
	if !strings.Contains(report.WhatChanged, "refactored y") {
		t.Fatalf("WhatChanged missing: %q", report.WhatChanged)
	}
	found := false
	for _, f := range report.FilesTouched {
		if strings.Contains(f, "x.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected x.go in FilesTouched, got %#v", report.FilesTouched)
	}
}

func TestFormatReport_IncludesAllSections(t *testing.T) {
	report := Report{
		Task:                "task1",
		WhatChanged:         "changed",
		Findings:            "found",
		FilesExamined:       []string{"a.go"},
		FilesTouched:        []string{"b.go"},
		OpenQuestions:       []string{"why?"},
		RecommendedNextStep: "ship it",
	}
	out := FormatReport(report)
	for _, want := range []string{"task1", "changed", "found", "a.go", "b.go", "why?", "ship it"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}
