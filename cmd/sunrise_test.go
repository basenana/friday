package main

import (
	"testing"
	"time"

	"github.com/basenana/friday/sessions"
)

func TestFilterOldSessions(t *testing.T) {
	today := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		sessions      []sessions.SessionMeta
		wantCount     int
		wantSessionID string
	}{
		{
			name:          "empty sessions",
			sessions:      []sessions.SessionMeta{},
			wantCount:     0,
			wantSessionID: "",
		},
		{
			name: "all sessions today",
			sessions: []sessions.SessionMeta{
				{ID: "session-today", CreatedAt: today},
			},
			wantCount:     0,
			wantSessionID: "",
		},
		{
			name: "all sessions old",
			sessions: []sessions.SessionMeta{
				{ID: "session-old", CreatedAt: today.Add(-24 * time.Hour)},
			},
			wantCount:     1,
			wantSessionID: "session-old",
		},
		{
			name: "mixed sessions",
			sessions: []sessions.SessionMeta{
				{ID: "session-old-1", CreatedAt: today.Add(-48 * time.Hour)},
				{ID: "session-today", CreatedAt: today},
				{ID: "session-old-2", CreatedAt: today.Add(-24 * time.Hour)},
			},
			wantCount: 2,
		},
		{
			name: "session exactly at midnight",
			sessions: []sessions.SessionMeta{
				{ID: "session-midnight", CreatedAt: today},
				{ID: "session-before", CreatedAt: today.Add(-time.Second)},
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterOldSessions(tt.sessions, today)
			if len(got) != tt.wantCount {
				t.Errorf("filterOldSessions() returned %d sessions, want %d", len(got), tt.wantCount)
			}
			if tt.wantSessionID != "" && (len(got) == 0 || got[0].ID != tt.wantSessionID) {
				t.Errorf("filterOldSessions() first session ID = %v, want %v", got[0].ID, tt.wantSessionID)
			}
		})
	}
}
