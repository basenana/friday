package utils

import "github.com/hyponet/eventbus/bus"

// Deprecated: integrations should depend on an explicit eventbus.Bus instead
// of the process-global compatibility aliases below.
var (
	Subscribe          = bus.Subscribe
	SubscribeOnce      = bus.SubscribeOnce
	SubscribeWithBlock = bus.SubscribeWithBlock
	Unsubscribe        = bus.Unsubscribe
	Publish            = bus.Publish
)
