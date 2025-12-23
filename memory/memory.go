package memory

import (
	"context"
	"fmt"
	"github.com/basenana/friday/vfs"
	"sync"

	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Memory struct {
	ctxID     string
	copyTimes int

	session Session

	history []types.Message
	mux     sync.Mutex

	tokens int64
	logger *zap.SugaredLogger
}

func (m *Memory) History() []types.Message {
	m.mux.Lock()
	defer m.mux.Unlock()
	result := make([]types.Message, 0, len(m.history)+1)
	result = append(result, m.history...)
	return result
}

func (m *Memory) AppendMessages(messages ...types.Message) {
	m.mux.Lock()
	defer m.mux.Unlock()

	for _, message := range messages {
		m.session.AppendMessage(context.TODO(), m.ctxID, &message)
		m.history = append(m.history, message)
		m.tokens += message.FuzzyTokens()
	}
	m.logger.Infow("append new messages", "fuzzyTokens", m.tokens, "ctxID", m.ctxID)
}

func (m *Memory) RunHook(ctx context.Context, hookName string) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	payload := &types.SessionPayload{
		ContextID: m.ctxID,
		History:   m.history,
	}
	err := m.session.RunHooks(ctx, hookName, payload)
	if err != nil {
		m.logger.Errorw("failed to run hook", "hookName", hookName, "err", err)
		return err
	}

	var newTokens int64
	for _, message := range payload.History {
		newTokens += message.FuzzyTokens()
	}
	m.tokens = newTokens
	m.history = payload.History
	return nil
}

func (m *Memory) RunBeforeModelHook(ctx context.Context) error {
	return m.RunHook(ctx, types.SessionHookBeforeModel)
}

func (m *Memory) RunAfterModelHook(ctx context.Context) error {
	return m.RunHook(ctx, types.SessionHookAfterModel)
}

func (m *Memory) VFS() vfs.VirtualFileSystem {
	return m.session.VFS()
}

func (m *Memory) Session() *types.Session {
	return m.session.Session()
}

func (m *Memory) Tools() []*tools.Tool {
	memoryTools := make([]*tools.Tool, 0)
	if v := m.session.VFS(); v != nil {
		memoryTools = append(memoryTools, tools.ReadTools(v)...)
	}
	return memoryTools
}

func (m *Memory) Tokens() int64 {
	return m.tokens
}

func (m *Memory) Copy() *Memory {
	m.mux.Lock()
	defer m.mux.Unlock()

	m.copyTimes += 1
	mid := fmt.Sprintf("%s.%d", m.ctxID, m.copyTimes)
	nm := Memory{
		ctxID:   mid,
		session: m.session,
		tokens:  m.tokens,
		logger:  m.logger,
	}

	nm.history = make([]types.Message, len(m.history))
	for i, msg := range m.history {
		nm.history[i] = msg
	}
	return &nm
}

type OptionSetter func(*Memory)

func NewEmpty(ctxID string, setters ...OptionSetter) *Memory {
	mem := &Memory{
		ctxID:   ctxID,
		session: newLimitedSession(ctxID),
		history: make([]types.Message, 0, 10), logger: logger.New("memory"),
	}

	for _, setter := range setters {
		setter(mem)
	}
	return mem
}

func New(session Session, setters ...OptionSetter) *Memory {
	mem := &Memory{
		ctxID:   session.ID(),
		session: session,
		history: make([]types.Message, 0, 10),
		logger:  logger.New("memory"),
	}

	for _, setter := range setters {
		setter(mem)
	}

	mem.history = session.History(context.TODO())
	return mem
}

func WithHistory(history ...types.Message) OptionSetter {
	return func(mem *Memory) {
		mem.history = history
	}
}
