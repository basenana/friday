package actor

import "github.com/basenana/friday/pkg/agent"

type Coordinator struct {
	agent *agent.Agent
	Inbox *Inbox
}
