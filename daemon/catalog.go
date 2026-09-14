package daemon

import (
	"context"
	"errors"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/sessions"
)

type SessionCatalog interface {
	Create(context.Context) (string, error)
	Exists(string) (bool, error)
	LoadEvents(context.Context, string) ([]events.Event, error)
}

type managerCatalog struct {
	manager *sessions.Manager
	events  sessions.EventStore
}

func NewSessionCatalog(manager *sessions.Manager) (SessionCatalog, error) {
	if manager == nil {
		return nil, errors.New("session manager is required")
	}
	eventStore, ok := manager.GetStore().(sessions.EventStore)
	if !ok {
		return nil, errors.New("session store does not support actor event history")
	}
	return &managerCatalog{manager: manager, events: eventStore}, nil
}

func (c *managerCatalog) Create(ctx context.Context) (string, error) {
	lifecycle, err := c.manager.CreateRoot(ctx, nil)
	if err != nil {
		return "", err
	}
	id := lifecycle.RootID()
	if err := lifecycle.Close(); err != nil {
		return "", err
	}
	return id, nil
}

func (c *managerCatalog) Exists(id string) (bool, error) { return c.manager.Exists(id) }

func (c *managerCatalog) LoadEvents(ctx context.Context, id string) ([]events.Event, error) {
	return c.events.LoadEvents(ctx, id)
}
