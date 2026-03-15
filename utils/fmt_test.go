package utils

import "testing"

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{
			name:     "plain json",
			response: `{"key": "value"}`,
			want:     `{"key": "value"}`,
		},
		{
			name:     "json in markdown code block",
			response: "```json\n{\"key\": \"value\"}\n```",
			want:     `{"key": "value"}`,
		},
		{
			name:     "json in generic code block",
			response: "```\n{\"key\": \"value\"}\n```",
			want:     `{"key": "value"}`,
		},
		{
			name:     "json with surrounding text",
			response: "Here is the result:\n{\"key\": \"value\"}\nDone",
			want:     `{"key": "value"}`,
		},
		{
			name:     "nested json",
			response: `{"outer": {"inner": "value"}}`,
			want:     `{"outer": {"inner": "value"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractJSON(tt.response)
			if got != tt.want {
				t.Errorf("ExtractJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}
