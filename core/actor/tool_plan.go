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
		coretools.WithDescription(`Ask the user one to three material questions and block until they answer or cancel.

Use question_1, question_2, and question_3 in display order. question_1 is
required. For a multiple-choice question, provide the matching options_1,
options_2, or options_3 array with 2–3 mutually exclusive answer strings.
Put the recommended option first and include its impact in the same string.
Omit the options field when a free-text answer is required.

Do not provide IDs, headers, option labels, option values, or an "Other"
choice. The UI generates those automatically.

Returns answers in the same order as the supplied questions.`),
		coretools.WithString("question_1", coretools.Required(), coretools.MinLength(1), coretools.Description("First question shown to the user. Required; use one short sentence.")),
		coretools.WithArray("options_1", questionOptions("question_1")...),
		coretools.WithString("question_2", coretools.MinLength(1), coretools.Description("Optional second question shown after question_1.")),
		coretools.WithArray("options_2", questionOptions("question_2")...),
		coretools.WithString("question_3", coretools.MinLength(1), coretools.Description("Optional third question shown after question_2.")),
		coretools.WithArray("options_3", questionOptions("question_3")...),
		coretools.WithExample(map[string]any{
			"question_1": "Which scope should we use?",
			"options_1":  []any{"Core tools first (Recommended) — lower risk and faster validation", "All built-ins — broader consistency but a larger change"},
			"question_2": "Which validation level should be required?",
			"options_2":  []any{"Schema and handler tests (Recommended) — covers contracts and behavior", "Schema tests only — faster but less coverage"},
		}),
		coretools.WithToolHandler(func(ctx context.Context, req *coretools.Request) (*coretools.Result, error) {
			schema := cards.FormSchema{Title: "Questions", Description: "Answer these questions to continue.", Variant: "questions"}
			questionCount := 0
			for index := 1; index <= 3; index++ {
				id := fmt.Sprintf("question_%d", index)
				question := strings.TrimSpace(stringArgument(req.Arguments, id))
				optionsKey := fmt.Sprintf("options_%d", index)
				if question == "" {
					if _, exists := req.Arguments[optionsKey]; exists {
						return coretools.NewToolResultError(fmt.Sprintf("%s requires %s", optionsKey, id)), nil
					}
					continue
				}
				if index > 1 && questionCount != index-1 {
					return coretools.NewToolResultError("questions must use consecutive numbers starting at question_1"), nil
				}
				questionCount++
				header := fmt.Sprintf("Question %d", index)
				field := cards.Field{Name: id, Label: header, Help: question, Type: cards.FieldText, Required: true}
				options, hasOptions := req.Arguments[optionsKey].([]any)
				if !hasOptions {
					schema.Fields = append(schema.Fields, field)
					continue
				}
				field.Type = cards.FieldSelect
				seenOptions := map[string]bool{"other": true}
				for _, raw := range options {
					option := strings.TrimSpace(raw.(string))
					key := strings.ToLower(option)
					if option == "" || seenOptions[key] {
						return coretools.NewToolResultError(fmt.Sprintf("%s must contain unique, non-empty strings and cannot include Other", optionsKey)), nil
					}
					seenOptions[key] = true
					field.Options = append(field.Options, cards.Option{Label: option, Value: option})
				}
				field.Options = append(field.Options, cards.Option{Label: "Write your own answer", Value: "Other"})
				schema.Fields = append(schema.Fields, field, cards.Field{Name: id + "_other", Label: header + " — Other", Type: cards.FieldText})
			}
			if questionCount == 0 {
				return coretools.NewToolResultError("question_1 is required"), nil
			}
			if err := cards.Default.ValidateForm(schema); err != nil {
				return coretools.NewToolResultError(err.Error()), nil
			}

			raw, _ := json.Marshal(schema)
			var schemaMap map[string]any
			_ = json.Unmarshal(raw, &schemaMap)
			formID := globalIDGenerator.Next("user-input")
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
			answers := make([]any, 0, questionCount)
			for index := 1; index <= questionCount; index++ {
				id := fmt.Sprintf("question_%d", index)
				value := outcome.Values[id]
				if value == "Other" {
					if other, _ := outcome.Values[id+"_other"].(string); strings.TrimSpace(other) != "" {
						value = other
					}
				}
				answers = append(answers, value)
			}
			encoded, _ := json.Marshal(map[string]any{"answers": answers})
			return coretools.NewToolResultText(string(encoded)), nil
		}),
	)
}

func questionOptions(question string) []coretools.PropertyOption {
	return []coretools.PropertyOption{
		coretools.MinItems(2), coretools.MaxItems(3), coretools.UniqueItems(true),
		coretools.Items(map[string]any{"type": "string", "minLength": 1}),
		coretools.Description("Two or three mutually exclusive answers for " + question + ". Omit for free text."),
	}
}

func makeEnterPlanModeTool(a *Actor) *coretools.Tool {
	return coretools.NewTool(collaboration.EnterPlanModeToolName,
		coretools.WithDescription("Enter Plan Mode when the task has material ambiguity, needs significant design decisions, or should be reviewed before implementation. Continue planning the same task after this tool returns."),
		coretools.WithToolHandler(func(_ context.Context, _ *coretools.Request) (*coretools.Result, error) {
			if a.modeController == nil {
				return coretools.NewToolResultError("collaboration mode changes are unavailable"), nil
			}
			if a.modeController.CollaborationMode(a.session.ID) == collaboration.ModePlan {
				return coretools.NewToolResultText("Already in Plan Mode."), nil
			}
			if err := a.modeController.SetMode(a.session.ID, collaboration.ModePlan); err != nil {
				return coretools.NewToolResultError("enter Plan Mode: " + err.Error()), nil
			}
			a.EmitCustom(events.CustomModeChanged, globalIDGenerator.Next("mode"), events.ModeChangedBody{
				Mode: string(collaboration.ModePlan), Source: "agent",
			})
			return coretools.NewToolResultText("Entered Plan Mode. Continue the same task under the Plan Mode instructions."), nil
		}),
	)
}

func makeSubmitPlanTool(a *Actor) *coretools.Tool {
	return coretools.NewTool(collaboration.SubmitPlanToolName,
		coretools.WithDescription("Submit the final decision-complete implementation plan. The first Markdown heading becomes the plan title. This ends the planning turn."),
		coretools.WithString("markdown", coretools.Required(), coretools.Description("Complete Markdown plan with Summary, Implementation Changes, Test Plan, and Assumptions")),
		coretools.WithToolHandler(func(_ context.Context, req *coretools.Request) (*coretools.Result, error) {
			if a.modeProvider == nil || a.modeProvider.CollaborationMode(a.session.ID) != collaboration.ModePlan {
				return coretools.NewToolResultError("submit_plan is only available in Plan Mode"), nil
			}
			markdown, _ := req.Arguments["markdown"].(string)
			markdown = strings.TrimSpace(markdown)
			if markdown == "" {
				return coretools.NewToolResultError("markdown is required"), nil
			}
			if len([]byte(markdown)) > 256<<10 {
				return coretools.NewToolResultError("plan markdown exceeds 256 KiB"), nil
			}
			for _, heading := range []string{"summary", "implementation changes", "test plan", "assumptions"} {
				if !hasMarkdownHeading(markdown, heading) {
					return coretools.NewToolResultError(fmt.Sprintf("plan is missing the %q section", heading)), nil
				}
			}
			plan := planning.Artifact{ID: globalIDGenerator.Next("plan"), SessionID: a.session.ID, Title: coretools.MarkdownTitle(markdown, "Implementation plan"), Markdown: markdown, Status: planning.ArtifactProposed, CreatedAt: time.Now()}
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

func stringArgument(arguments map[string]any, name string) string {
	value, _ := arguments[name].(string)
	return value
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
