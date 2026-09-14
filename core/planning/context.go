package planning

import (
	"context"

	"github.com/basenana/friday/core/promptcontext"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

const (
	approvedPlanOpen  = "<approved-plan>\n"
	approvedPlanClose = "\n</approved-plan>"
	approvedPlanGuide = `The following is the approved implementation plan for the current task.

Treat this plan as the authoritative execution guide. Continue from the current
progress and advance the remaining work according to the plan. Verify completed
steps and keep implementation decisions aligned with its goals and constraints.

You may adjust tactical details when required by the actual codebase, but do not
silently abandon or materially change the plan. If the plan becomes infeasible,
conflicts with the user's latest request, or requires a material change, explain
the issue and ask for direction when necessary.

Do not merely restate the plan. Use it to continue and complete the work.`
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
	if err != nil {
		return err
	}
	if !ok {
		promptcontext.SetBlock(req, promptcontext.ApprovedPlan, "")
		return nil
	}
	promptcontext.SetBlock(req, promptcontext.ApprovedPlan, approvedPlanContext(markdown))
	return nil
}

// ReservedTokens returns the request budget occupied by the stable accepted
// plan block. It intentionally swallows repository errors; BeforeModel is the
// authoritative error path and will prevent an unplanned model call.
func (h *ApprovedPlanContextHook) ReservedTokens(sess *session.Session) int64 {
	markdown, ok, err := h.approvedMarkdown(sess)
	if err != nil || !ok {
		return 0
	}
	return session.EstimateHistoryTokens([]types.Message{{
		Role:    types.RoleAgent,
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
	return approvedPlanOpen + approvedPlanGuide + "\n\n" + markdown + approvedPlanClose
}

var _ session.BeforeModelHook = (*ApprovedPlanContextHook)(nil)
