package flow

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/basenana/go-flow/exec"
	goflow "github.com/basenana/go-flow/flow"
	"github.com/basenana/go-flow/utils"
	"github.com/google/uuid"

	"friday/flow/operator"
	"friday/flow/task"
)

var (
	storage goflow.Storage
	ctrl    *goflow.Controller
)

func init() {
	register := exec.NewLocalOperatorBuilderRegister()
	_ = register.Register("IngestOperator", operator.NewIngestOperator)

	goflow.RegisterExecutorBuilder("local", func(flow *goflow.Flow) goflow.Executor {
		return exec.NewLocalExecutor(flow, register)
	})
	storage = goflow.NewInMemoryStorage()
	ctrl = goflow.NewFlowController(storage)
}

type Manager struct {
	binDir string
	log    utils.Logger
}

func NewManager(binDir string) *Manager {
	return &Manager{
		binDir: binDir,
		log:    utils.NewLogger("flow"),
	}
}

func (m *Manager) NewIngestFlow(id string, knowledgeDir string) (goflow.Flow, error) {
	elementOutput := filepath.Join(m.binDir, "element.json")
	elementTask := task.NewElementTask(m.binDir, knowledgeDir, elementOutput)

	ingestTask, err := task.NewIngestTask(elementOutput)
	if err != nil {
		return goflow.Flow{}, err
	}

	// set task dependency
	elementTask.Next.OnSucceed = ingestTask.Name

	return goflow.Flow{
		ID:            id,
		Describe:      "Ingest knowledge.",
		Executor:      "local",
		ControlPolicy: goflow.ControlPolicy{FailedPolicy: goflow.PolicyFastFailed},
		Tasks:         []goflow.Task{elementTask, ingestTask},
	}, nil
}

func (m *Manager) Ingest(ctx context.Context, knowledgeDir string) (err error) {
	// build flow
	var flow goflow.Flow
	flowId := uuid.New().String()
	flow, err = m.NewIngestFlow(flowId, knowledgeDir)
	if err != nil {
		return
	}

	// run
	return m.run(ctx, &flow)
}

func (m *Manager) run(ctx context.Context, flow *goflow.Flow) (err error) {
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