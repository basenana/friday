package flow

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	goflow "github.com/basenana/go-flow/flow"
	"github.com/basenana/go-flow/utils"
	"github.com/google/uuid"

	"friday/config"
	"friday/flow/task"
)

type ShellManager struct {
	binDir       string
	fridayConfig config.Config
	log          utils.Logger
}

func NewShellManager(binDir string, fridayConfig config.Config) *ShellManager {
	return &ShellManager{
		binDir:       binDir,
		fridayConfig: fridayConfig,
		log:          utils.NewLogger("flow"),
	}
}

func (m *ShellManager) NewIngestFlowInShell(id string, knowledgeDir string) (goflow.Flow, error) {
	elementOutput := filepath.Join(m.binDir, "element.json")
	elementTask := task.NewElementTask(m.binDir, knowledgeDir, elementOutput)
	configTask, err := task.NewConfigTask(m.binDir, m.fridayConfig)
	if err != nil {
		return goflow.Flow{}, err
	}
	ingestTask, err := task.NewIngestTaskInShell(m.binDir, elementOutput)
	if err != nil {
		return goflow.Flow{}, err
	}

	// set task dependency
	elementTask.Next.OnSucceed = configTask.Name
	configTask.Next.OnSucceed = ingestTask.Name

	return goflow.Flow{
		ID:            id,
		Describe:      "Ingest knowledge.",
		Executor:      "local",
		ControlPolicy: goflow.ControlPolicy{FailedPolicy: goflow.PolicyFastFailed},
		Tasks:         []goflow.Task{elementTask, configTask, ingestTask},
	}, nil
}

func (m *ShellManager) NewQuestionFlow(id string, question string) (goflow.Flow, error) {
	configTask, err := task.NewConfigTask(m.binDir, m.fridayConfig)
	if err != nil {
		return goflow.Flow{}, err
	}
	questionTask, err := task.NewQuestionTask(m.binDir, question)
	if err != nil {
		return goflow.Flow{}, err
	}

	// set task dependency
	configTask.Next.OnSucceed = questionTask.Name

	return goflow.Flow{
		ID:            id,
		Describe:      "Question based on knowledge.",
		Executor:      "local",
		ControlPolicy: goflow.ControlPolicy{FailedPolicy: goflow.PolicyFastFailed},
		Tasks:         []goflow.Task{configTask, questionTask},
	}, nil
}

func (m *ShellManager) Question(ctx context.Context, question string) (err error) {
	// build flow
	var flow goflow.Flow
	flowId := uuid.New().String()
	flow, err = m.NewQuestionFlow(flowId, question)
	if err != nil {
		return
	}

	// run
	return m.run(ctx, &flow)
}

func (m *ShellManager) run(ctx context.Context, flow *goflow.Flow) (err error) {
	flowId := flow.ID
	// save flow
	if err = storage.SaveFlow(ctx, flow); err != nil {
		return
	}

	// run flow
	err = ctrl.TriggerFlow(ctx, flowId)
	if err != nil {
		return
	}

	// check flow status
	var t *time.Ticker
	t = time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var f *goflow.Flow
			f, err = storage.GetFlow(ctx, flowId)
			if err != nil {
				return
			}
			if f.Status == goflow.SucceedStatus {
				m.log.Infof("flow %s succeed", flowId)
				return
			}
			if f.Status == goflow.FailedStatus {
				m.log.Errorf("flow %s failed", flowId)
				err = fmt.Errorf("flow %s failed: %s", flowId, f.Message)
				return
			}
			if f.Status == goflow.ErrorStatus {
				m.log.Errorf("flow %s error", flowId)
				err = fmt.Errorf("flow %s errored: %s", flowId, f.Message)
				return
			}
			if f.Status == goflow.CanceledStatus {
				m.log.Errorf("flow %s canceled", flowId)
				err = fmt.Errorf("flow %s canceled", flowId)
				return
			}
		}
	}
}
