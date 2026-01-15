package sessions

import (
	"context"
	"fmt"
	"sync"

	"github.com/basenana/friday/core/types"
	"github.com/google/uuid"
)

type store struct {
	mu       sync.RWMutex
	sessions map[string]*types.Session
	messages map[string][]*types.Message
}

var _ SessionStore = &store{}

func NewStore() SessionStore {
	return &store{
		sessions: make(map[string]*types.Session),
		messages: make(map[string][]*types.Message),
	}
}

func copySession(s *types.Session) *types.Session {
	if s == nil {
		return nil
	}
	metadata := make(map[string]string)
	if s.Metadata != nil {
		for k, v := range s.Metadata {
			metadata[k] = v
		}
	}
	return &types.Session{
		ID:       s.ID,
		Type:     s.Type,
		Metadata: metadata,
		System:   s.System,
		Purpose:  s.Purpose,
		Summary:  s.Summary,
		Report:   s.Report,
	}
}

func copyMessage(m *types.Message) *types.Message {
	if m == nil {
		return nil
	}
	metadata := make(map[string]string)
	for k, v := range m.Metadata {
		metadata[k] = v
	}
	return &types.Message{
		SystemMessage:      m.SystemMessage,
		UserMessage:        m.UserMessage,
		AgentMessage:       m.AgentMessage,
		AssistantMessage:   m.AssistantMessage,
		AssistantReasoning: m.AssistantReasoning,
		ImageURL:           m.ImageURL,
		ToolCallID:         m.ToolCallID,
		ToolName:           m.ToolName,
		ToolArguments:      m.ToolArguments,
		ToolContent:        m.ToolContent,
		Metadata:           metadata,
		Time:               m.Time,
	}
}

func (s *store) ListSessions(ctx context.Context, filter map[string]string, includesClosed bool) ([]*types.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*types.Session
	for _, session := range s.sessions {
		state := session.Metadata[types.MetadataSessionState]
		isClosed := state == types.MetadataSessionStateClosed
		if !includesClosed && isClosed {
			continue
		}

		match := true
		for k, v := range filter {
			if session.Metadata[k] != v {
				match = false
				break
			}
		}
		if match {
			result = append(result, copySession(session))
		}
	}
	return result, nil
}

func (s *store) CreateSession(ctx context.Context, session *types.Session) (*types.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newSession := copySession(session)
	if newSession.ID == "" {
		newSession.ID = uuid.New().String()
	}
	s.sessions[newSession.ID] = newSession
	s.messages[newSession.ID] = []*types.Message{}
	return newSession, nil
}

func (s *store) UpdateSession(ctx context.Context, session *types.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[session.ID]; !ok {
		return fmt.Errorf("session not found: %s", session.ID)
	}
	s.sessions[session.ID] = copySession(session)
	return nil
}

func (s *store) OpenSession(ctx context.Context, sessionID string) (*types.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return copySession(session), nil
}

func (s *store) AppendMessages(ctx context.Context, sessionID string, message ...*types.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	for _, m := range message {
		s.messages[sessionID] = append(s.messages[sessionID], copyMessage(m))
	}
	return nil
}

func (s *store) ListMessages(ctx context.Context, sessionID string) ([]*types.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	msgs, ok := s.messages[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	result := make([]*types.Message, len(msgs))
	for i, m := range msgs {
		result[i] = copyMessage(m)
	}
	return result, nil
}

func (s *store) CloseSession(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if session.Metadata == nil {
		session.Metadata = make(map[string]string)
	}
	session.Metadata[types.MetadataSessionState] = types.MetadataSessionStateClosed
	return nil
}
