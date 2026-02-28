package file

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	f "github.com/basenana/friday/fs"
)

type SessionMeta struct {
	ID           string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	Summary      string    `json:"summary,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
}

type FileSessionStore struct {
	basePath string
	sessions map[string]*session.Session
	llm      interface {
		Completion(ctx interface{}, req interface{}) interface{}
	}
}

func NewFileSessionStore(basePath string) *FileSessionStore {
	return &FileSessionStore{
		basePath: basePath,
		sessions: make(map[string]*session.Session),
	}
}

func (s *FileSessionStore) EnsureDir() error {
	return os.MkdirAll(s.basePath, 0755)
}

func (s *FileSessionStore) MetaPath(sessionID string) string {
	return filepath.Join(s.basePath, fmt.Sprintf("%s.json", sessionID))
}

func (s *FileSessionStore) historyPath(sessionID string) string {
	return filepath.Join(s.basePath, fmt.Sprintf("%s.jsonl", sessionID))
}

func (s *FileSessionStore) Create(sessionID string, llm interface {
	Completion(ctx interface{}, req interface{}) interface{}
}, opts ...session.Option) (*session.Session, error) {
	if err := s.EnsureDir(); err != nil {
		return nil, err
	}

	now := time.Now()
	meta := SessionMeta{
		ID:           sessionID,
		CreatedAt:    now,
		UpdatedAt:    now,
		MessageCount: 0,
	}

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(s.MetaPath(sessionID), metaData, 0644); err != nil {
		return nil, err
	}

	if err := os.WriteFile(s.historyPath(sessionID), []byte{}, 0644); err != nil {
		return nil, err
	}

	workdir := f.NewFileSystem("")
	sess := session.New(sessionID, nil, append(opts, session.WithWorkdirFS(workdir))...)
	s.sessions[sessionID] = sess

	return sess, nil
}

func (s *FileSessionStore) Load(sessionID string, llm interface {
	Completion(ctx interface{}, req interface{}) interface{}
}, opts ...session.Option) (*session.Session, error) {
	metaPath := s.MetaPath(sessionID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s", sessionID)
		}
		return nil, err
	}

	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}

	workdir := f.NewFileSystem("")
	sess := session.New(sessionID, nil, append(opts, session.WithWorkdirFS(workdir))...)

	messages, err := s.LoadMessages(sessionID)
	if err != nil {
		return nil, err
	}
	var msgPtrs []*types.Message
	for i := range messages {
		msgPtrs = append(msgPtrs, &messages[i])
	}
	sess.AppendMessage(msgPtrs...)

	s.sessions[sessionID] = sess
	return sess, nil
}

func (s *FileSessionStore) List() ([]SessionMeta, error) {
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionMeta{}, nil
		}
		return nil, err
	}

	var result []SessionMeta
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		sessionID := strings.TrimSuffix(entry.Name(), ".json")
		metaPath := s.MetaPath(sessionID)

		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var meta SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		result = append(result, meta)
	}

	return result, nil
}

func (s *FileSessionStore) AppendMessages(sessionID string, msgs ...types.Message) error {
	historyPath := s.historyPath(sessionID)

	file, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, msg := range msgs {
		record := toRecord(msg)
		data, err := json.Marshal(record)
		if err != nil {
			continue
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			continue
		}
	}

	s.updateMeta(sessionID, len(msgs))

	return nil
}

func (s *FileSessionStore) LoadMessages(sessionID string) ([]types.Message, error) {
	historyPath := s.historyPath(sessionID)

	data, err := os.ReadFile(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []types.Message{}, nil
		}
		return nil, err
	}

	var messages []types.Message
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var rec messageRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		messages = append(messages, rec.toMessage())
	}

	return messages, nil
}

func (s *FileSessionStore) updateMeta(sessionID string, added int) error {
	metaPath := s.MetaPath(sessionID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}

	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}

	meta.UpdatedAt = time.Now()
	meta.MessageCount += added

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metaPath, metaData, 0644)
}

type messageRecord struct {
	Role               string `json:"role"`
	SystemMessage      string `json:"system_message,omitempty"`
	UserMessage        string `json:"user_message,omitempty"`
	AgentMessage       string `json:"agent_message,omitempty"`
	AssistantMessage   string `json:"assistant_message,omitempty"`
	AssistantReasoning string `json:"assistant_reasoning,omitempty"`
	ImageURL           string `json:"image_url,omitempty"`
	ToolCallID         string `json:"tool_call_id,omitempty"`
	ToolName           string `json:"tool_name,omitempty"`
	ToolArguments      string `json:"tool_arguments,omitempty"`
	ToolContent        string `json:"tool_content,omitempty"`
	Time               string `json:"time,omitempty"`
}

func toRecord(msg types.Message) messageRecord {
	role := "user"
	if msg.SystemMessage != "" {
		role = "system"
	} else if msg.AgentMessage != "" || msg.AssistantMessage != "" {
		role = "assistant"
	} else if msg.ToolName != "" {
		role = "tool"
	}

	return messageRecord{
		Role:               role,
		SystemMessage:      msg.SystemMessage,
		UserMessage:        msg.UserMessage,
		AgentMessage:       msg.AgentMessage,
		AssistantMessage:   msg.AssistantMessage,
		AssistantReasoning: msg.AssistantReasoning,
		ImageURL:           msg.ImageURL,
		ToolCallID:         msg.ToolCallID,
		ToolName:           msg.ToolName,
		ToolArguments:      msg.ToolArguments,
		ToolContent:        msg.ToolContent,
		Time:               msg.Time,
	}
}

func (r messageRecord) toMessage() types.Message {
	return types.Message{
		SystemMessage:      r.SystemMessage,
		UserMessage:        r.UserMessage,
		AgentMessage:       r.AgentMessage,
		AssistantMessage:   r.AssistantMessage,
		AssistantReasoning: r.AssistantReasoning,
		ImageURL:           r.ImageURL,
		ToolCallID:         r.ToolCallID,
		ToolName:           r.ToolName,
		ToolArguments:      r.ToolArguments,
		ToolContent:        r.ToolContent,
		Time:               r.Time,
	}
}
