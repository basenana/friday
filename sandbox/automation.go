package sandbox

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/basenana/friday/core/providers"
)

const automationAssessTimeout = 15 * time.Second

type CommandMode string

const (
	CommandForeground CommandMode = "foreground"
	CommandBackground CommandMode = "background"
)

type CommandRequest struct {
	Command   string
	Workdir   string
	Mode      CommandMode
	SessionID string
}

type AutomationAssessment struct {
	RequiresApproval bool
	Risks            map[string]float64
}

type CommandAutomation struct {
	provider  providers.DecisionProvider
	threshold float64
}

type commandAutomationState struct {
	Command  string      `json:"command"`
	Workdir  string      `json:"workdir"`
	Mode     CommandMode `json:"mode"`
	Commands []Command   `json:"commands"`
}

func NewCommandAutomation(provider providers.DecisionProvider, threshold float64) (*CommandAutomation, error) {
	if provider == nil {
		return nil, errors.New("command automation requires a decision provider")
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 || threshold > 1 {
		return nil, errors.New("command automation threshold must be between 0 and 1")
	}
	return &CommandAutomation{provider: provider, threshold: threshold}, nil
}

func (a *CommandAutomation) Assess(ctx context.Context, req CommandRequest) (AutomationAssessment, error) {
	if err := ctx.Err(); err != nil {
		return AutomationAssessment{}, err
	}
	commands, err := ParseCommands(req.Command)
	if err != nil {
		return AutomationAssessment{}, fmt.Errorf("parse command for automation: %w", err)
	}

	questions := map[string]providers.DecisionQuestion{
		"environment_damage": providers.NoulQuestion{
			Instructions: "This command would damage or pollute the local execution environment, system configuration, installed dependencies, permissions, or running state.",
			Criteria: &providers.NoulCriteria{
				True:  "The command can make harmful or hard-to-reverse changes to the local execution environment; ordinary command failure alone does not count.",
				False: "The command only reads state or makes routine, scoped, reversible project changes and does not endanger the local environment.",
			},
		},
		"data_loss": providers.NoulQuestion{
			Instructions: "This command could delete, overwrite, or irreversibly modify local data.",
			Criteria: &providers.NoulCriteria{
				True:  "Local files or data may be deleted, overwritten, truncated, or irreversibly changed.",
				False: "The command does not risk local data loss; ordinary command failure alone does not count.",
			},
		},
		"remote_side_effect": providers.NoulQuestion{
			Instructions: "This command would push, publish, deploy, upload, send, or modify a remote repository, service, infrastructure resource, or other external system.",
			Criteria: &providers.NoulCriteria{
				True:  "The command can create or modify externally visible remote state.",
				False: "The command is local-only or read-only with respect to remote systems; a failed network request alone does not count.",
			},
		},
	}

	assessCtx, cancel := context.WithTimeout(ctx, automationAssessTimeout)
	defer cancel()
	response, err := a.provider.Evaluate(assessCtx, providers.DecisionRequest{
		State: commandAutomationState{
			Command:  req.Command,
			Workdir:  req.Workdir,
			Mode:     req.Mode,
			Commands: commands,
		},
		Questions: questions,
		SessionID: req.SessionID,
	})
	if err != nil {
		return AutomationAssessment{}, err
	}

	risks := make(map[string]float64, len(questions))
	requiresApproval := false
	for key := range questions {
		answer, ok := response.Answers[key]
		if !ok {
			return AutomationAssessment{}, fmt.Errorf("decision response missing %q", key)
		}
		noul, ok := answer.(providers.NoulAnswer)
		if !ok {
			return AutomationAssessment{}, fmt.Errorf("decision response %q has type %T, want providers.NoulAnswer", key, answer)
		}
		if math.IsNaN(noul.Noul) || math.IsInf(noul.Noul, 0) || noul.Noul < 0 || noul.Noul > 1 {
			return AutomationAssessment{}, fmt.Errorf("decision response %q has invalid probability %v", key, noul.Noul)
		}
		risks[key] = noul.Noul
		if noul.Noul >= a.threshold {
			requiresApproval = true
		}
	}
	return AutomationAssessment{RequiresApproval: requiresApproval, Risks: risks}, nil
}
