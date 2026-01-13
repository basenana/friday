package sessions

import (
	"context"

	"github.com/basenana/friday/core/types"
)

type HookHandler func(ctx context.Context, payload *types.SessionPayload) error

type Hooks interface {
	GetHooks() map[string][]HookHandler
}

func (d *Descriptor) RegisterHooks(hooks Hooks) {
	next := hooks.GetHooks()
	if len(next) == 0 {
		return
	}
	d.mux.Lock()
	defer d.mux.Unlock()
	if d.hooks == nil {
		d.hooks = make(map[string][]HookHandler)
	}

	for hookName, hookFuncs := range next {
		d.hooks[hookName] = append(d.hooks[hookName], hookFuncs...)
	}
}

func (d *Descriptor) RunHooks(ctx context.Context, hookName string, payload *types.SessionPayload) error {
	if d.hooks == nil {
		return nil
	}
	d.mux.Lock()
	hooks, ok := d.hooks[hookName]
	d.mux.Unlock()

	if !ok || len(hooks) == 0 {
		return nil
	}

	for _, h := range hooks {
		if err := h(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}
