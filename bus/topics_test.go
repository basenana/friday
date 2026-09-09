package bus

import (
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/core/actor/events"
)

func TestSessionIDCannotInjectTopicWildcard(t *testing.T) {
	b := eventbus.NewBus()
	var attacker, victim atomic.Int32
	attackerIDs := SubscribeAgent(b, "*", func(Envelope) { attacker.Add(1) }, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	victimIDs := SubscribeAgent(b, "victim", func(Envelope) { victim.Add(1) }, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})

	topic := TopicReplyContent("victim")
	b.Publish(topic, Envelope{Event: events.NewEvent(events.KindTextMessageContent, "run"), Topic: topic, Session: "victim"})
	UnsubscribeAll(b, attackerIDs...)
	UnsubscribeAll(b, victimIDs...)
	b.Wait()

	if got := victim.Load(); got != 1 {
		t.Fatalf("victim received %d events, want 1", got)
	}
	if got := attacker.Load(); got != 0 {
		t.Fatalf("wildcard session received %d events from another session", got)
	}
}

func TestSubscribeAgentReceivesDottedObservabilityEvents(t *testing.T) {
	b := eventbus.NewBus()
	var (
		mu  sync.Mutex
		got []string
	)
	ids := SubscribeAgent(b, "s1", func(env Envelope) {
		mu.Lock()
		got = append(got, env.Name)
		mu.Unlock()
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})

	want := []string{events.CustomCompactStart, events.CustomSubagentFinish, events.CustomTodoUpdate, events.CustomLoopStart, "step"}
	for _, name := range want {
		topic := TopicObs("s1", name)
		b.Publish(topic, Envelope{Event: events.NewEvent(events.KindCustom, "run").WithName(name), Topic: topic, Session: "s1"})
	}
	UnsubscribeAll(b, ids...)
	b.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("received observability events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("observability order = %v, want %v", got, want)
		}
	}
}

func TestHybridClockIsMonotonicAcrossCollisionsAndClockRegression(t *testing.T) {
	var clock hybridClock
	base := time.UnixMilli(1_000)
	first := clock.next(base)
	second := clock.next(base)
	third := clock.next(base.Add(-time.Hour))
	if !(first < second && second < third) {
		t.Fatalf("timestamps are not monotonic: %d, %d, %d", first, second, third)
	}

	const callers = 256
	values := make([]int64, callers)
	var wg sync.WaitGroup
	for i := range values {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			values[i] = clock.next(base)
		}(i)
	}
	wg.Wait()
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	for i := 1; i < len(values); i++ {
		if values[i] <= values[i-1] {
			t.Fatalf("concurrent timestamps are not unique: %d then %d", values[i-1], values[i])
		}
	}
}
