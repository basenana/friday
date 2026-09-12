package planning

import (
	"context"
	"strings"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

const (
	approvedPlanOpen  = "<approved_plan>\n"
	approvedPlanClose = "\n</approved_plan>\n\n"
)

// ApprovedPlanContextHook injects the latest accepted plan into model
// requests for a persisted root session. The injection is request-local: the
// plan remains authoritative in Repository and is never copied into history.
type ApprovedPlanContextHook struct {
	plans Repository
}

func NewApprovedPlanContextHook(plans Repository) *ApprovedPlanContextHook {
	return &ApprovedPlanContextHook{plans: plans}
}

func (h *ApprovedPlanContextHook) BeforeModel(_ context.Context, sess *session.Session, req providers.Request) error {
	markdown, ok, err := h.approvedMarkdown(sess)
	if err != nil || !ok {
		return err
	}

	prefix := approvedPlanContext(markdown)
	history := append([]types.Message(nil), req.History()...)
	for i := range history {
		if history[i].Role != types.RoleUser {
			continue
		}
		if !strings.HasPrefix(history[i].Content, prefix) {
			history[i].Content = prefix + history[i].Content
			history[i].Tokens = 0
		}
		req.SetHistory(history)
		return nil
	}

	req.SetHistory(append([]types.Message{{Role: types.RoleUser, Content: strings.TrimSuffix(prefix, "\n\n")}}, history...))
	return nil
}

// ReservedTokens returns the request budget occupied by the stable accepted
// plan prefix. It intentionally swallows repository errors; BeforeModel is the
// authoritative error path and will prevent an unplanned model call.
func (h *ApprovedPlanContextHook) ReservedTokens(sess *session.Session) int64 {
	markdown, ok, err := h.approvedMarkdown(sess)
	if err != nil || !ok {
		return 0
	}
	return session.EstimateHistoryTokens([]types.Message{{
		Role:    types.RoleUser,
		Content: approvedPlanContext(markdown),
	}})
}

func (h *ApprovedPlanContextHook) approvedMarkdown(sess *session.Session) (string, bool, error) {
	if h == nil || h.plans == nil || sess == nil || sess.Temporary || sess.Root != sess {
		return "", false, nil
	}
	plan, err := h.plans.LoadLatestPlan(sess.ID)
	if err != nil {
		return "", false, err
	}
	if plan == nil || plan.Status != ArtifactAccepted {
		return "", false, nil
	}
	return plan.Markdown, true, nil
}

func approvedPlanContext(markdown string) string {
	return approvedPlanOpen + markdown + approvedPlanClose
}

var _ session.BeforeModelHook = (*ApprovedPlanContextHook)(nil)
