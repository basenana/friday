package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageCombination(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		stdin       string
		wantContain []string
	}{
		{
			name:        "args only",
			args:        []string{"hello", "world"},
			stdin:       "",
			wantContain: []string{"hello world"},
		},
		{
			name:        "stdin only",
			args:        nil,
			stdin:       "stdin content",
			wantContain: []string{"stdin content"},
		},
		{
			name:        "combined args and stdin",
			args:        []string{"summarize", "errors"},
			stdin:       "error line 1\nerror line 2",
			wantContain: []string{"summarize errors", "error line 1", "error line 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var argMessage, stdinMessage string

			if len(tt.args) > 0 {
				argMessage = strings.Join(tt.args, " ")
			}
			stdinMessage = tt.stdin

			var userMessage string
			switch {
			case argMessage != "" && stdinMessage != "":
				userMessage = argMessage + "\n\n" + stdinMessage
			case argMessage != "":
				userMessage = argMessage
			case stdinMessage != "":
				userMessage = stdinMessage
			}

			for _, want := range tt.wantContain {
				if !strings.Contains(userMessage, want) {
					t.Errorf("message should contain %q, got %q", want, userMessage)
				}
			}
		})
	}
}

func TestNormalizeImageRef(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "sample.png")
	if err := os.WriteFile(imagePath, []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
		0x54, 0x08, 0x99, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := normalizeImageRef(imagePath)
	if err != nil {
		t.Fatalf("normalizeImageRef() error = %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("normalizeImageRef() = %q, want absolute path", got)
	}
}
