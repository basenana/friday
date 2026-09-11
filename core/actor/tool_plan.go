package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/basenana/friday/core/actor/cards"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	coretools "github.com/basenana/friday/core/tools"
)

func makeRequestUserInputTool(a *Actor) *coretools.Tool {
	return coretools.NewTool(collaboration.RequestUserInputToolName,
		coretools.WithDescription("Ask one to three short, material planning questions and wait for structured user answers."),
		coretools.WithArray("questions", coretools.Required(), coretools.Items(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":       map[string]any{"type": "string"},
				"header":   map[string]any{"type": "string"},
				"question": map[string]any{"type": "string"},
				"options":  map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
			},
			"required": []string{"id", "header", "question", "options"},
		})),
		coretools.WithToolHandler(func(ctx context.Context, req *coretools.Request) (*coretools.Result, error) {
			if a.modeProvider == nil || a.modeProvider.CollaborationMode(a.session.ID) != collaboration.ModePlan {
				return coretools.NewToolResultError("request_user_input is only available in Plan Mode"), nil
			}
			raw, ok := req.Arguments["questions"].([]any)
			if !ok || len(raw) == 0 || len(raw) > 3 {
				return coretools.NewToolResultError("questions must contain 1 to 3 items"), nil
			}
			schema := cards.FormSchema{Title: "Planning questions", Description: "Answer these choices to continue the plan.", Variant: "plan_questions"}
			seen := map[string]bool{}
			for _, item := range raw {
				q, ok := item.(map[string]any)
				if !ok {
					return coretools.NewToolResultError("each question must be an object"), nil
				}
				id, _ := q["id"].(string)
				header, _ := q["header"].(string)
				question, _ := q["question"].(string)
				id, header, question = strings.TrimSpace(id), strings.TrimSpace(header), strings.TrimSpace(question)
				options, _ := q["options"].([]any)
				if !validQuestionID(id) || seen[id] || header == "" || question == "" || len(options) < 2 || len(options) > 3 {
					return coretools.NewToolResultError("question ids must be unique and each question needs a header, text, and 2 to 3 options"), nil
				}
				if len([]rune(header)) > 12 {
					return coretools.NewToolResultError("question headers cannot exceed 12 characters"), nil
				}
				seen[id] = true
				field := cards.Field{Name: id, Label: header, Help: question, Type: cards.FieldSelect, Required: true}
				seenLabels := map[string]bool{"other": true}
				for _, optionRaw := range options {
					option, ok := optionRaw.(map[string]any)
					if !ok {
						return coretools.NewToolResultError("each option must be an object"), nil
					}
					label, _ := option["label"].(string)
					description, _ := option["description"].(string)
					label, description = strings.TrimSpace(label), strings.TrimSpace(description)
					labelKey := strings.ToLower(label)
					if label == "" || seenLabels[labelKey] {
						return coretools.NewToolResultError("option labels must be non-empty, unique, and cannot be Other"), nil
					}
					seenLabels[labelKey] = true
					field.Options = append(field.Options, cards.Option{Label: label, Value: label, Description: description})
				}
				field.Options = append(field.Options, cards.Option{Label: "Other", Value: "Other", Description: "Provide a different answer in the text field that appears below."})
				schema.Fields = append(schema.Fields, field, cards.Field{Name: id + "_other", Label: header + " — Other", Type: cards.FieldText})
			}
			if err := cards.Default.ValidateForm(schema); err != nil {
				return coretools.NewToolResultError(err.Error()), nil
			}

			schemaMap, err := formSchemaToMap(schema)
			if err != nil {
				return coretools.NewToolResultError(err.Error()), nil
			}
			formID := globalIDGenerator.Next("plan-input")
			waiter := a.prepareFormWait(formID)
			a.EmitCustom(events.CustomFormRequested, formID, events.FormRequestedBody{FormID: formID, Schema: schemaMap})
			outcome, err := a.waitForRegisteredForm(ctx, formID, waiter)
			if err != nil {
				a.EmitCustom(events.CustomFormCancelled, formID, events.FormCancelledBody{FormID: formID})
				return coretools.NewToolResultError("input wait failed: " + err.Error()), nil
			}
			if outcome.Cancelled {
				if !outcome.cancelEventEmitted {
					a.EmitCustom(events.CustomFormCancelled, formID, events.FormCancelledBody{FormID: formID})
				}
				return coretools.NewToolResultText(`{"cancelled":true}`), nil
			}
			a.EmitCustom(events.CustomFormSubmitted, formID, events.FormSubmittedBody{FormID: formID, Values: outcome.Values})
			answers := map[string]any{}
			for id := range seen {
				value := outcome.Values[id]
				if value == "Other" {
					if other, _ := outcome.Values[id+"_other"].(string); strings.TrimSpace(other) != "" {
						value = other
					}
				}
				answers[id] = map[string]any{"answers": []any{value}}
			}
			encoded, _ := json.Marshal(map[string]any{"answers": answers})
			return coretools.NewToolResultText(string(encoded)), nil
		}),
	)
}

func makeEnterPlanModeTool(a *Actor) *coretools.Tool {
	return coretools.NewTool(collaboration.EnterPlanModeToolName,
		coretools.WithDescription("Enter Plan Mode when the task has material ambiguity, needs significant design decisions, or should be reviewed before implementation. Continue planning the same task after this tool returns."),
		coretools.WithString("reason", coretools.Description("Brief reason planning and user approval are warranted")),
		coretools.WithToolHandler(func(_ context.Context, req *coretools.Request) (*coretools.Result, error) {
			if a.modeController == nil {
				return coretools.NewToolResultError("collaboration mode changes are unavailable"), nil
			}
			if a.modeController.CollaborationMode(a.session.ID) == collaboration.ModePlan {
				return coretools.NewToolResultText("Already in Plan Mode."), nil
			}
			reason, _ := req.Arguments["reason"].(string)
			reason = strings.TrimSpace(reason)
			if err := a.modeController.SetMode(a.session.ID, collaboration.ModePlan); err != nil {
				return coretools.NewToolResultError("enter Plan Mode: " + err.Error()), nil
			}
			a.EmitCustom(events.CustomModeChanged, globalIDGenerator.Next("mode"), events.ModeChangedBody{
				Mode: string(collaboration.ModePlan), Source: "agent", Reason: reason,
			})
			return coretools.NewToolResultText("Entered Plan Mode. Continue the same task under the Plan Mode instructions."), nil
		}),
	)
}

func validQuestionID(id string) bool {
	if id == "" || strings.HasSuffix(id, "_other") || id[0] < 'a' || id[0] > 'z' {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func makeSubmitPlanTool(a *Actor) *coretools.Tool {
	return coretools.NewTool(collaboration.SubmitPlanToolName,
		coretools.WithDescription("Submit the final decision-complete implementation plan. This ends the planning turn."),
		coretools.WithString("title", coretools.Required(), coretools.Description("Concise plan title")),
		coretools.WithString("markdown", coretools.Required(), coretools.Description("Complete Markdown plan with Summary, Implementation Changes, Test Plan, and Assumptions")),
		coretools.WithToolHandler(func(_ context.Context, req *coretools.Request) (*coretools.Result, error) {
			if a.modeProvider == nil || a.modeProvider.CollaborationMode(a.session.ID) != collaboration.ModePlan {
				return coretools.NewToolResultError("submit_plan is only available in Plan Mode"), nil
			}
			title, _ := req.Arguments["title"].(string)
			markdown, _ := req.Arguments["markdown"].(string)
			title, markdown = strings.TrimSpace(title), strings.TrimSpace(markdown)
			if title == "" || markdown == "" {
				return coretools.NewToolResultError("title and markdown are required"), nil
			}
			if len([]byte(markdown)) > 256<<10 {
				return coretools.NewToolResultError("plan markdown exceeds 256 KiB"), nil
			}
			for _, heading := range []string{"summary", "implementation changes", "test plan", "assumptions"} {
				if !hasMarkdownHeading(markdown, heading) {
					return coretools.NewToolResultError(fmt.Sprintf("plan is missing the %q section", heading)), nil
				}
			}
			plan := planning.Artifact{ID: globalIDGenerator.Next("plan"), SessionID: a.session.ID, Title: title, Markdown: markdown, Status: planning.ArtifactProposed, CreatedAt: time.Now()}
			saved, err := a.planRepository.ProposePlan(a.session.ID, plan)
			if err != nil {
				return coretools.NewToolResultError("save plan: " + err.Error()), nil
			}
			a.EmitCustom(events.CustomPlanProposed, saved.ID, events.PlanProposedBody{PlanID: saved.ID, Version: saved.Version, Title: saved.Title, Markdown: saved.Markdown})
			a.planSubmitted.Store(true)
			return coretools.NewToolResultText(fmt.Sprintf("Plan %s version %d submitted.", saved.ID, saved.Version)), nil
		}),
	)
}

func hasMarkdownHeading(markdown, want string) bool {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		level := 0
		for level < len(line) && line[level] == '#' {
			level++
		}
		if level == 0 || level > 6 || level == len(line) || (line[level] != ' ' && line[level] != '\t') {
			continue
		}
		heading := strings.TrimSpace(line[level:])
		heading = strings.TrimSpace(strings.TrimRight(heading, "#"))
		if strings.EqualFold(heading, want) {
			return true
		}
	}
	return false
}
