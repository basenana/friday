package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

const (
	compactThreshold    = 100 * 1000
	abstractThreshold   = 150 * 1000
	toolResultKeyPrefix = "/tool/result"
)

type Memory struct {
	mid       string
	copyTimes int

	system  *types.Message
	history []types.Message
	mux     sync.Mutex

	sum *summarizer

	storage Storage
	tokens  int64
	logger  *zap.SugaredLogger
}

func (m *Memory) History() []types.Message {
	m.mux.Lock()
	defer m.mux.Unlock()
	if m.tokens > compactThreshold {
		m.logger.Warn("history limit exceeded, try to compact", "mid", m.mid)
		m.compactMessages()
	}
	result := make([]types.Message, 0, len(m.history)+1)
	result = append(result, *m.system)
	result = append(result, m.history...)
	return result
}

func (m *Memory) AppendMessages(messages ...types.Message) {
	m.mux.Lock()
	defer m.mux.Unlock()
	for _, message := range messages {
		m.history = append(m.history, message)
		m.tokens += message.FuzzyTokens()
	}
	m.logger.Infow("append new messages", "fuzzyTokens", m.tokens, "mid", m.mid)
}

func (m *Memory) Tokens() int64 {
	return m.tokens
}

func (m *Memory) compactMessages() {
	var (
		beforeTokens = m.tokens
		afterTokens  = m.system.FuzzyTokens()
		msgLen       = len(m.history)
		canbeCompact = msgLen / 2
	)

	for i, msg := range m.history {
		if i > canbeCompact {
			afterTokens += msg.FuzzyTokens()
			continue
		}

		if msg.ToolCallID != "" && msg.OriginToolContent == "" && len(msg.ToolContent) > 100 {
			fk := fmt.Sprintf("%s/%s.txt", toolResultKeyPrefix, msg.ToolCallID)
			_ = m.storage.Replace(context.Background(), fk, msg.ToolContent)

			msg.OriginToolContent = msg.ToolContent
			msg.ToolContent = remindMessage(fk)
			afterTokens += msg.FuzzyTokens()
			m.history[i] = msg
			continue
		}

		afterTokens += msg.FuzzyTokens()
	}

	m.tokens = afterTokens

	compressionRatio := float64(beforeTokens-afterTokens) / float64(beforeTokens)
	m.logger.Infow("compact messages finish",
		"beforeTokens", beforeTokens, "afterTokens", afterTokens, "compressionRatio", compressionRatio, "mid", m.mid)
	if m.sum != nil && (compressionRatio < 0.1 || m.tokens > abstractThreshold) {
		m.logger.Warn("compact history messages radically", "mid", m.mid)
		go m.sum.doSummarize(m.history)
	}
}

func (m *Memory) updateHistoryWithAbstract(history []types.Message, abstract string, err error) {
	if err != nil {
		m.logger.Errorw("failed to abstract history", "mid", m.mid, "error", err)
		return
	}

	m.mux.Lock()
	defer m.mux.Unlock()

	m.logger.Infow("abstract history", "abstract", abstract, "mid", m.mid)

	var (
		cutAt      = len(history)
		crtLen     = len(m.history)
		beforToken = m.tokens
	)

	if crtLen-cutAt < 5 { // keep 5 newest message
		cutAt = crtLen - 5
	}
	if cutAt < 0 {
		cutAt = 0
	}
	keep := m.history[cutAt:]
	m.history = m.history[:0]

	m.tokens = m.system.FuzzyTokens()
	m.history = append(m.history, types.Message{UserMessage: abstract})
	m.history = append(m.history, keep...)
	for _, msg := range m.history {
		m.tokens += msg.FuzzyTokens()
	}
	m.logger.Infow("update history with abstract finished",
		"beforeHistory", crtLen, "afterHistory", len(m.history),
		"beforeToken", beforToken, "afterToken", m.tokens, "mid", m.mid)
}

func (m *Memory) Reset(system string, cleanHistory bool) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.system = &types.Message{SystemMessage: system}
	m.tokens = m.system.FuzzyTokens()
	if cleanHistory {
		m.history = make([]types.Message, 0, 10)
	}
	for _, msg := range m.history {
		m.tokens += msg.FuzzyTokens()
	}
}

func (m *Memory) Copy() *Memory {
	m.mux.Lock()
	defer m.mux.Unlock()

	m.copyTimes += 1
	mid := fmt.Sprintf("%s.%d", m.mid, m.copyTimes)
	nm := Memory{
		mid:     mid,
		history: make([]types.Message, len(m.history)),
		storage: m.storage,
		tokens:  m.tokens,
		logger:  m.logger,
	}
	for i, msg := range m.history {
		nm.history[i] = msg
	}
	return &nm
}

func NewEmpty(sessionID string) *Memory {
	return &Memory{
		mid:     sessionID,
		history: make([]types.Message, 0, 10),
		storage: newInMemoryStorage(),
		logger:  logger.New("memory"),
	}
}

func NewEmptyWithSummarize(sessionID string, llmCli openai.Client) *Memory {
	m := NewEmpty(sessionID)
	if llmCli == nil {
		return m
	}
	m.sum = newSummarize(llmCli, m.updateHistoryWithAbstract)
	return m
}
