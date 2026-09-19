package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/cards"
	"github.com/basenana/friday/core/actor/events"
)

// ApprovalDecision is the user's answer to a sandbox approval form.
type ApprovalDecision int

const (
	// ApprovalDeny keeps the command denied.
	ApprovalDeny ApprovalDecision = iota
	// ApprovalOnce allows the command for the current session only.
	ApprovalOnce
	// ApprovalPersist allows the command for the whole project (written to
	// the HOME-side project allow file) and for the current session.
	ApprovalPersist
)

// maxApprovalRounds bounds how many approval forms one tool call may raise.
// A compound command can surface several distinct unknown sub-commands; the
// cap prevents the tool from spinning on prompts forever.
const maxApprovalRounds = 3

// Approval form field/option values.
const (
	approvalFieldDecision   = "decision"
	approvalValuePersist    = "persist"
	approvalValueOnce       = "once"
	approvalValueDeny       = "deny"
	approvalFormVariant     = "permission"
	approvalFormTitle       = "Sandbox 命令授权"
	approvalPersistLabelFmt = "为本项目永久授权（推荐）— %q 将保存到 %s，本项目后续会话不再询问"
	approvalOnceLabelFmt    = "仅本次授权 — 只允许当前会话使用 %q"
	approvalDenyLabel       = "拒绝"
)

// HeadlessSuggestion is the remediation hint surfaced when a command is
// denied and no interactive approval prompt is available.
const HeadlessSuggestion = "run `friday sandbox allow <command>` in the project directory, or retry in an interactive session to trigger the approval prompt"

// ErrApprovalDenied marks errors returned when the user declines (or cancels)
// a sandbox approval request.
var ErrApprovalDenied = errors.New("sandbox approval denied by user")

// IsApprovalDenied reports whether err is (or wraps) ErrApprovalDenied.
func IsApprovalDenied(err error) bool {
	return errors.Is(err, ErrApprovalDenied)
}

// userDenied wraps a denial the user explicitly declined so callers can
// distinguish "user said no" from "no interactive session available" while
// errors.Is(err, ErrPermissionDenied) keeps holding.
func userDenied(denied *DeniedError) error {
	return fmt.Errorf("%w: %w", ErrApprovalDenied, denied)
}

// FormPrompter is the narrow actor surface the approval flow needs. It is
// satisfied by *coreactor.Actor (EmitCustom and WaitForForm are public), so
// the approval flow drives forms without any changes to the core module.
type FormPrompter interface {
	// EmitCustom publishes a Custom event (form.requested / submitted /
	// cancelled) to the actor's subscribers and sink.
	EmitCustom(name, itemID string, payload any)
	// WaitForForm registers a pending form and blocks until the user submits
	// or cancels (or ctx expires).
	WaitForForm(ctx context.Context, formID string) (coreactor.FormOutcome, error)
}

// CommandApprover turns missing-allowlist denials into interactive approval
// requests. When the user approves, the grant is applied to the live
// Permission (effective immediately) and, for persist decisions, written to
// the HOME-side project allow file so future sessions start with the grant.
// Explicit deny-rule denials never prompt.
type CommandApprover struct {
	perm        *Permission
	overlayPath string

	mu       sync.Mutex
	prompter FormPrompter
}

// NewCommandApprover builds an approver over perm. overlayPath is the
// HOME-side project allow file written by persist decisions; when empty,
// persist decisions fail instead of writing to an undefined location. The
// approver starts unbound; call Bind to enable interactive approval.
func NewCommandApprover(perm *Permission, overlayPath string) *CommandApprover {
	return &CommandApprover{perm: perm, overlayPath: overlayPath}
}

// Bind attaches the interactive prompter (the session actor). Without one —
// headless runs, daemons — Request degrades to the headless denial error.
func (a *CommandApprover) Bind(p FormPrompter) {
	a.mu.Lock()
	a.prompter = p
	a.mu.Unlock()
}

// Prompter reports the currently bound prompter, if any.
func (a *CommandApprover) Prompter() FormPrompter {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prompter
}

// Request executes command through exec, requesting interactive approval
// when the command is denied only because it is missing from the allow list.
// After an approval the command is retried immediately within the same call,
// so the tool returns real command output without an extra model round trip.
// Explicit deny-rule denials and non-permission errors are returned as-is.
func (a *CommandApprover) Request(ctx context.Context, exec *Executor, command string, opts ExecOptions) (*Result, error) {
	result, err := exec.Run(ctx, command, opts)
	for attempts := 0; ; attempts++ {
		if err == nil {
			return result, nil
		}
		var denied *DeniedError
		if !errors.As(err, &denied) {
			return result, err
		}
		if denied.ExplicitDeny {
			return result, denied
		}
		if attempts >= maxApprovalRounds {
			return result, fmt.Errorf("command %q is still not in the allow list after %d approval rounds: %w", denied.Command, maxApprovalRounds, denied)
		}

		decision, derr := a.approve(ctx, denied)
		if derr != nil {
			return result, derr
		}
		switch decision {
		case ApprovalPersist:
			if a.overlayPath == "" {
				return result, fmt.Errorf("cannot persist approval for %q: project allow path is unavailable", denied.Command)
			}
			if perr := AppendProjectAllow(a.overlayPath, denied.Command); perr != nil {
				return result, fmt.Errorf("persist sandbox approval for %q: %w", denied.Command, perr)
			}
			a.perm.Grant(denied.Command)
		case ApprovalOnce:
			a.perm.Grant(denied.Command)
		default:
			return result, denied
		}
		result, err = exec.Run(ctx, command, opts)
	}
}

// approve raises the approval form and waits for the user's decision.
func (a *CommandApprover) approve(ctx context.Context, denied *DeniedError) (ApprovalDecision, error) {
	a.mu.Lock()
	prompter := a.prompter
	a.mu.Unlock()
	if prompter == nil {
		return ApprovalDeny, &DeniedError{
			Command: denied.Command,
			Reason:  fmt.Sprintf("command %q is not in allow list and interactive approval is unavailable", denied.Command),
		}
	}

	formID := nextApprovalFormID()
	schema := approvalFormSchema(denied.Command, a.overlayPath)
	schemaMap, err := marshalFormSchema(schema)
	if err != nil {
		return ApprovalDeny, err
	}

	prompter.EmitCustom(events.CustomFormRequested, formID, events.FormRequestedBody{FormID: formID, Schema: schemaMap})
	outcome, err := prompter.WaitForForm(ctx, formID)
	if err != nil {
		prompter.EmitCustom(events.CustomFormCancelled, formID, events.FormCancelledBody{FormID: formID})
		return ApprovalDeny, fmt.Errorf("approval wait failed: %w", err)
	}
	if outcome.Cancelled {
		prompter.EmitCustom(events.CustomFormCancelled, formID, events.FormCancelledBody{FormID: formID})
		return ApprovalDeny, userDenied(denied)
	}
	prompter.EmitCustom(events.CustomFormSubmitted, formID, events.FormSubmittedBody{FormID: formID, Values: outcome.Values})

	switch value, _ := outcome.Values[approvalFieldDecision].(string); value {
	case approvalValuePersist:
		return ApprovalPersist, nil
	case approvalValueOnce:
		return ApprovalOnce, nil
	default:
		return ApprovalDeny, userDenied(denied)
	}
}

// approvalFormSchema builds the single-question approval form. The
// recommended option (persist for the project) comes first.
func approvalFormSchema(command, overlayPath string) cards.FormSchema {
	return cards.FormSchema{
		Title:       approvalFormTitle,
		Description: fmt.Sprintf("命令 %q 不在沙箱允许列表中。选择如何处理：", command),
		Variant:     approvalFormVariant,
		SubmitLabel: "提交",
		Fields: []cards.Field{{
			Name:     approvalFieldDecision,
			Label:    "授权决定",
			Type:     cards.FieldSelect,
			Required: true,
			Options: []cards.Option{
				{Label: fmt.Sprintf(approvalPersistLabelFmt, command, overlayPath), Value: approvalValuePersist},
				{Label: fmt.Sprintf(approvalOnceLabelFmt, command), Value: approvalValueOnce},
				{Label: approvalDenyLabel, Value: approvalValueDeny},
			},
		}},
	}
}

func marshalFormSchema(schema cards.FormSchema) (map[string]any, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var schemaMap map[string]any
	if err := json.Unmarshal(raw, &schemaMap); err != nil {
		return nil, err
	}
	return schemaMap, nil
}

var approvalFormSeq atomic.Uint64

func nextApprovalFormID() string {
	return fmt.Sprintf("sandbox-approval-%d-%d", time.Now().UnixNano(), approvalFormSeq.Add(1))
}
