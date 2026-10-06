package sandbox

import (
	"context"
	"errors"
	"fmt"
)

// CommandGate combines static permissions, optional model-assisted risk
// assessment, and the existing interactive approval flow.
type CommandGate struct {
	exec       *Executor
	approver   *CommandApprover
	automation *CommandAutomation
}

func NewCommandGate(exec *Executor, approver *CommandApprover, automation *CommandAutomation) *CommandGate {
	return &CommandGate{exec: exec, approver: approver, automation: automation}
}

func (g *CommandGate) Authorize(ctx context.Context, req CommandRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.exec.config.Sandbox.Enabled && !g.exec.sandbox.IsAvailable() {
		return fmt.Errorf("%w: %s", ErrSandboxUnavailable, g.exec.sandbox.Name())
	}
	decision, err := g.exec.perm.CheckWithReason(req.Command)
	if err == nil && decision == Allow {
		return nil
	}
	var denied *DeniedError
	if !errors.As(err, &denied) {
		return err
	}
	if denied.ExplicitDeny {
		return denied
	}

	if g.automation == nil {
		if req.Mode == CommandBackground || g.approver == nil {
			return denied
		}
		return g.approver.Authorize(ctx, req.Command)
	}

	assessment, assessErr := g.automation.Assess(ctx, req)
	if assessErr == nil && !assessment.RequiresApproval {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.approver == nil {
		return denied
	}
	return g.approver.Authorize(ctx, req.Command)
}
