package summarize

import (
	"context"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
)

type Hook struct {
	llm              providers.Client
	compactThreshold int64
}

var _ session.BeforeModelHook = &Hook{}

func (h Hook) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	if sess.Tokens() < h.compactThreshold {
		return nil
	}

	newSession := sess.Fork()
	newSession.CleanHooks()
	agt := New(h.llm, Option{})
	stream := agt.Chat(ctx, &api.Request{Session: newSession})
	abstract, err := api.ReadAllContent(ctx, stream)
	if err != nil {
		return err
	}

	newHistory := session.RebuildHistoryWithAbstract(sess.GetHistory(), abstract)

	// Persist new history (automatically backs up original file)
	if err := sess.ReplaceHistory(newHistory...); err != nil {
		return err
	}

	req.SetHistory(sess.GetHistory())
	return nil
}

func NewCompactHook(llm providers.Client, compactThreshold int64) *Hook {
	return &Hook{llm: llm, compactThreshold: compactThreshold}
}
