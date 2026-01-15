package sessions

import (
	"context"
	"testing"

	"github.com/basenana/friday/core/types"
)

func TestCreateSession(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	session, err := s.CreateSession(ctx, &types.Session{
		Type:     types.SessionTypeChat,
		Metadata: map[string]string{"key": "value"},
		System:   "test system",
		Purpose:  "test purpose",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if session.ID == "" {
		t.Error("session ID should not be empty")
	}
	if session.Type != types.SessionTypeChat {
		t.Errorf("expected type %s, got %s", types.SessionTypeChat, session.Type)
	}
}

func TestCreateSessionWithID(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	session, err := s.CreateSession(ctx, &types.Session{
		ID:   "custom-id",
		Type: types.SessionTypeAgentic,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if session.ID != "custom-id" {
		t.Errorf("expected ID 'custom-id', got '%s'", session.ID)
	}
}

func TestListSessions(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	s.CreateSession(ctx, &types.Session{
		ID:       "s1",
		Type:     types.SessionTypeChat,
		Metadata: map[string]string{"env": "test"},
	})
	s.CreateSession(ctx, &types.Session{
		ID:       "s2",
		Type:     types.SessionTypeAgentic,
		Metadata: map[string]string{"env": "prod"},
	})

	list, err := s.ListSessions(ctx, map[string]string{"env": "test"}, false)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 session, got %d", len(list))
	}
	if list[0].ID != "s1" {
		t.Errorf("expected session s1, got %s", list[0].ID)
	}
}

func TestListSessionsIncludesClosed(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	s.CreateSession(ctx, &types.Session{ID: "s1"})
	s.CreateSession(ctx, &types.Session{ID: "s2"})
	s.CloseSession(ctx, "s2")

	list, err := s.ListSessions(ctx, nil, false)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 session (excluding closed), got %d", len(list))
	}

	list, err = s.ListSessions(ctx, nil, true)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 sessions (including closed), got %d", len(list))
	}
}

func TestUpdateSession(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	s.CreateSession(ctx, &types.Session{
		ID:       "s1",
		Purpose:  "old purpose",
	})

	err := s.UpdateSession(ctx, &types.Session{
		ID:      "s1",
		Purpose: "new purpose",
	})
	if err != nil {
		t.Fatalf("UpdateSession failed: %v", err)
	}

	session, _ := s.OpenSession(ctx, "s1")
	if session.Purpose != "new purpose" {
		t.Errorf("expected purpose 'new purpose', got '%s'", session.Purpose)
	}
}

func TestUpdateSessionNotFound(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	err := s.UpdateSession(ctx, &types.Session{ID: "nonexistent"})
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestOpenSession(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	s.CreateSession(ctx, &types.Session{
		ID:   "s1",
		Type: types.SessionTypeChat,
	})

	session, err := s.OpenSession(ctx, "s1")
	if err != nil {
		t.Fatalf("OpenSession failed: %v", err)
	}
	if session.ID != "s1" {
		t.Errorf("expected session s1, got %s", session.ID)
	}
}

func TestOpenSessionNotFound(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	_, err := s.OpenSession(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestAppendMessages(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	s.CreateSession(ctx, &types.Session{ID: "s1"})

	err := s.AppendMessages(ctx, "s1",
		&types.Message{UserMessage: "hello"},
		&types.Message{UserMessage: "world"},
	)
	if err != nil {
		t.Fatalf("AppendMessages failed: %v", err)
	}

	messages, err := s.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].UserMessage != "hello" {
		t.Errorf("expected 'hello', got '%s'", messages[0].UserMessage)
	}
}

func TestAppendMessagesNotFound(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	err := s.AppendMessages(ctx, "nonexistent", &types.Message{UserMessage: "test"})
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestListMessagesNotFound(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	_, err := s.ListMessages(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestCloseSession(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	s.CreateSession(ctx, &types.Session{ID: "s1"})

	err := s.CloseSession(ctx, "s1")
	if err != nil {
		t.Fatalf("CloseSession failed: %v", err)
	}

	session, err := s.OpenSession(ctx, "s1")
	if err != nil {
		t.Fatalf("OpenSession failed: %v", err)
	}
	if session.Metadata[types.MetadataSessionState] != types.MetadataSessionStateClosed {
		t.Errorf("expected state 'closed', got '%s'", session.Metadata[types.MetadataSessionState])
	}
}

func TestCloseSessionNotFound(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	err := s.CloseSession(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestExternalModification(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	original := &types.Session{
		ID:       "s1",
		Type:     types.SessionTypeChat,
		Metadata: map[string]string{"key": "original"},
	}
	s.CreateSession(ctx, original)

	original.Purpose = "modified"
	original.Metadata["newkey"] = "newvalue"

	session, _ := s.OpenSession(ctx, "s1")
	if session.Purpose == "modified" {
		t.Error("internal state should not be affected by external modification")
	}
	if _, ok := session.Metadata["newkey"]; ok {
		t.Error("internal metadata should not be affected by external modification")
	}
	if session.Metadata["key"] == "newvalue" {
		t.Error("internal metadata value should not be changed")
	}
}

func TestMessageExternalModification(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	s.CreateSession(ctx, &types.Session{ID: "s1"})
	s.AppendMessages(ctx, "s1", &types.Message{UserMessage: "hello"})

	msg, _ := s.ListMessages(ctx, "s1")
	msg[0].UserMessage = "modified"

	messages, _ := s.ListMessages(ctx, "s1")
	if messages[0].UserMessage == "modified" {
		t.Error("internal messages should not be affected by external modification")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	s.CreateSession(ctx, &types.Session{ID: "s1"})

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			s.AppendMessages(ctx, "s1", &types.Message{UserMessage: "test"})
			s.ListMessages(ctx, "s1")
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	messages, _ := s.ListMessages(ctx, "s1")
	if len(messages) != 10 {
		t.Errorf("expected 10 messages, got %d", len(messages))
	}
}
