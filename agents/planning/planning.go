package planning

import (
	"context"
	"fmt"
	"github.com/basenana/friday/tools"
	"sort"

	"github.com/basenana/friday/agents"
	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/agents/react"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Agent struct {
	react *react.Agent

	name   string
	desc   string
	llm    openai.Client
	opt    Option
	todo   *TodoList
	pt     []*tools.Tool
	logger *zap.SugaredLogger
}

var _ agents.Agent = &Agent{}

func (a *Agent) Name() string {
	return a.name
}

func (a *Agent) Describe() string {
	return a.desc
}

func (a *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	return a.react.Chat(ctx, &agtapi.Request{
		UserMessage: fmt.Sprintf("%s\n%s", displayTodoList(a.todo), req.UserMessage),
		Memory:      req.Memory,
	})
}

func (a *Agent) PlanningTools() []*tools.Tool {
	if a.pt != nil {
		return a.pt
	}
	a.pt = newPlanningTool(a)
	return a.pt
}

func (a *Agent) TodoList() []TodoListItem {
	orderList := a.todo.list()
	sort.Slice(orderList, func(i, j int) bool {
		return orderList[i].ID < orderList[j].ID
	})
	return orderList
}

func (a *Agent) AllFinish() bool {
	for _, item := range a.TodoList() {
		if !item.IsFinish {
			return false
		}
	}
	return true
}

func (a *Agent) SetTodoDone(todoID int32) {
	_ = a.todo.update(fmt.Sprintf("%d", todoID), "done")
}

func New(name, desc string, llm openai.Client, option Option) *Agent {
	if option.SystemPrompt == "" {
		option.SystemPrompt = DEFAULT_PLANNING_PROMPT
	}

	agt := &Agent{
		name:   name,
		desc:   desc,
		llm:    llm,
		opt:    option,
		todo:   emptyTodoList(),
		logger: logger.New("planning"),
	}

	option.Tools = append(option.Tools, agt.PlanningTools()...)

	agt.react = react.New(name, desc, llm, react.Option{
		SystemPrompt: option.SystemPrompt,
		MaxLoopTimes: 5,
		MaxToolCalls: 10,
		Tools:        option.Tools,
	})
	return agt
}

type Option struct {
	SystemPrompt string
	Tools        []*tools.Tool
}
