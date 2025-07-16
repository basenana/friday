package actor

import "github.com/basenana/friday/pkg/agent"

type Expert struct {
	agent *agent.Agent
	mail  *Mailbox
}
