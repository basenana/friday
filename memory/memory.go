package memory

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

var (
	compactThreshold  int64 = 80 * 1000
	abstractThreshold int64 = 110 * 1000
)

func init() {
	if ctStr := os.Getenv("FRIDAY_MEMORY_COMPACT_THRESHOLD"); ctStr != "" {
		ct, _ := strconv.ParseInt(ctStr, 10, 64)
		if ct > 1000 {
			compactThreshold = ct
		}
	}
	if atStr := os.Getenv("FRIDAY_MEMORY_ABSTRACT_THRESHOLD"); atStr != "" {
		at, _ := strconv.ParseInt(atStr, 10, 64)
		if at > 1000 {
			abstractThreshold = at
		}
	}
}

type Memory struct {
	mid       string
	copyTimes int

	history []types.Message
	mux     sync.Mutex

	sum       *summarizer
	notebook  Notebook
	recorders []Recorder

	tokens int64
	logger *zap.SugaredLogger
}

func (m *Memory) History() []types.Message {
	m.mux.Lock()
	defer m.mux.Unlock()
	if m.tokens > compactThreshold {
		m.logger.Warnw("history limit exceeded, try to compact", "mid", m.mid)
		m.compactMessages()
	}
	result := make([]types.Message, 0, len(m.history)+1)
	result = append(result, m.history...)
	return result
}

func (m *Memory) AppendMessages(messages ...types.Message) {
	m.mux.Lock()
	defer m.mux.Unlock()
	nowAt := time.Now().Format(time.RFC3339)

	for _, message := range messages {
		if message.Time == "" {
			message.Time = nowAt
		}
		for _, record := range m.recorders {
			record.Record(message)
		}
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
		beforeTokens, afterTokens int64 = m.tokens, 0
		newHistory                []types.Message
		needKeepIdx               = theIndexAfterKeep(len(m.history))
	)

	for i, msg := range m.history {
		if msg.AgentMessage != "" {
			continue
		}

		if i < needKeepIdx && msg.ToolCallID != "" && msg.SimplifiedToolContent == "" && len(msg.ToolContent) > 100 {
			n, err := m.notebook.SaveOrUpdate(context.Background(), &Note{
				Title:   "tool-result-" + msg.ToolCallID,
				Content: msg.ToolContent,
			})
			if err != nil {
				m.logger.Errorw("save note for compact error", "err", err.Error())
				afterTokens += msg.FuzzyTokens()
				newHistory = append(newHistory, msg)
				continue
			}

			msg.SimplifiedToolContent = remindMessage(n.ID)
			afterTokens += msg.FuzzyTokens()
			newHistory = append(newHistory, msg)
			continue
		}

		afterTokens += msg.FuzzyTokens()
		newHistory = append(newHistory, msg)
	}

	m.tokens = afterTokens
	m.history = newHistory

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

	if abstract == "" {
		m.logger.Errorw("abstract history is empty", "mid", m.mid)
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

	m.tokens = 0
	m.history = append(m.history, types.Message{AgentMessage: summaryPrefix + abstract})
	m.history = append(m.history, keep...)
	for _, msg := range m.history {
		m.tokens += msg.FuzzyTokens()
	}
	m.logger.Infow("update history with abstract finished",
		"beforeHistory", crtLen, "afterHistory", len(m.history),
		"beforeToken", beforToken, "afterToken", m.tokens, "mid", m.mid)
}

func (m *Memory) Tools() []*tools.Tool {
	memoryTools := make([]*tools.Tool, 0)
	if m.notebook != nil {
		memoryTools = append(memoryTools, m.notebook.ReadTools()...)
	}
	return memoryTools
}

func (m *Memory) Copy() *Memory {
	m.mux.Lock()
	defer m.mux.Unlock()

	m.copyTimes += 1
	mid := fmt.Sprintf("%s.%d", m.mid, m.copyTimes)
	nm := Memory{
		mid:      mid,
		history:  make([]types.Message, len(m.history)),
		sum:      m.sum,
		notebook: m.notebook,
		tokens:   m.tokens,
		logger:   m.logger,
	}
	for i, msg := range m.history {
		nm.history[i] = msg
	}
	return &nm
}

func theIndexAfterKeep(msgLen int) int {
	mid := msgLen / 2
	keep5 := msgLen - 5
	if keep5 > msgLen {
		return keep5
	}
	return mid
}

type OptionSetter func(*Memory)

func NewEmpty(uid string, setters ...OptionSetter) *Memory {
	mem := &Memory{
		mid:      uid,
		history:  make([]types.Message, 0, 10),
		notebook: NewInMemoryNotebook(),
		logger:   logger.New("memory"),
	}

	for _, setter := range setters {
		setter(mem)
	}
	return mem
}

func WithSummarize(llmCli openai.Client) OptionSetter {
	return func(m *Memory) {
		if llmCli == nil {
			return
		}
		m.sum = newSummarize(llmCli, m.updateHistoryWithAbstract)
	}
}

func WithNotebook(notebook Notebook) OptionSetter {
	return func(m *Memory) {
		m.notebook = notebook
	}
}

func WithRecorders(recorders ...Recorder) OptionSetter {
	return func(m *Memory) {
		m.recorders = append(m.recorders, recorders...)
	}
}
