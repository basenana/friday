package bridge

import (
	"context"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

type bridgePlanRuntime struct{ mode collaboration.Mode }

func (r *bridgePlanRuntime) CollaborationMode(string) collaboration.Mode { return r.mode }
func (r *bridgePlanRuntime) SetMode(_ string, mode collaboration.Mode) error {
	r.mode = mode
	return nil
}

type bridgePlanRepo struct{ latest *planning.Artifact }

func (r *bridgePlanRepo) ProposePlan(_ string, plan planning.Artifact) (*planning.Artifact, error) {
	plan.Version = 1
	r.latest = &plan
	return &plan, nil
}
func (r *bridgePlanRepo) SavePlan(_ string, plan planning.Artifact) error {
	r.latest = &plan
	return nil
}
func (r *bridgePlanRepo) LoadPlan(_, _ string) (*planning.Artifact, error)  { return r.latest, nil }
func (r *bridgePlanRepo) LoadLatestPlan(string) (*planning.Artifact, error) { return r.latest, nil }

type bridgeSubmitPlanAgent struct{}

func (bridgeSubmitPlanAgent) Chat(ctx context.Context, req *api.Request) *api.Response {
	resp := api.NewResponse()
	go func() {
		defer resp.Close()
		for _, tool := range req.Tools {
			if tool.Name != collaboration.SubmitPlanToolName {
				continue
			}
			_, _ = tool.Handler(ctx, &tools.Request{SessionID: req.Session.ID, Arguments: map[string]any{
				"title":    "Bridge plan",
				"markdown": "## Summary\nS\n## Implementation Changes\nC\n## Test Plan\nT\n## Assumptions\nA",
			}})
			return
		}
	}()
	return resp
}

func TestOutBridgeDeliversPlanBeforeRunFinished(t *testing.T) {
	b := eventbus.NewBus()
	runtime := &bridgePlanRuntime{mode: collaboration.ModePlan}
	a := coreactor.New(bridgeSubmitPlanAgent{}, coresession.New("s1", nil), coreactor.WithPlanning(runtime, &bridgePlanRepo{}))
	feed := bus.SubscribeAgentFeed(b, "s1")
	bridge := NewOutBridge(b, "s1", a)
	a.Start(context.Background())
	defer func() {
		a.Stop()
		bridge.Close()
		feed.Close()
		b.Wait()
	}()

	if err := a.Send(context.Background(), coreactor.UserTextMessage{Text: "plan", TurnID: "run-plan"}); err != nil {
		t.Fatal(err)
	}
	var sawPlan bool
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-feed.Events():
			if evt.Name == events.CustomPlanProposed {
				sawPlan = true
			}
			if evt.Type != events.KindRunFinished {
				continue
			}
			var data events.RunFinishedData
			if err := events.DecodePayload(evt, &data); err != nil {
				t.Fatal(err)
			}
			if !sawPlan || data.StopReason != "plan_completed" {
				t.Fatalf("plan delivery/order: sawPlan=%v finished=%+v", sawPlan, data)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for bridged plan lifecycle")
		}
	}
}
