package coordinator

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/types"
)

type request struct {
	req  *agtapi.Request
	resp *agtapi.Response
}

func (r *request) recordMail(agentName, title, text, reply string) {
	r.req.Memory.AppendMessages(types.Message{
		AssistantMessage: fmt.Sprintf("To: %s\nTitle: %s\n\n%s", agentName, title, text)})
	r.req.Memory.AppendMessages(types.Message{UserMessage: reply})
}

func withRequest(ctx context.Context, req *request) context.Context {
	return context.WithValue(ctx, "agent.coor.request", req)
}

func requestFromContext(ctx context.Context) *request {
	return ctx.Value("agent.coor.request").(*request)
}

func fuzzyMatching(s1, s2 string) bool {
	s1 = strings.ToLower(strings.ReplaceAll(s1, " ", ""))
	s2 = strings.ToLower(strings.ReplaceAll(s2, " ", ""))
	return s1 == s2
}
