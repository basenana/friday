package state

import (
	"context"
	"errors"
	"sync"
)

type inMemory struct {
	mu     sync.RWMutex
	app    map[string]string
	users  map[string]map[string]string
}

func NewInMemory() State {
	return &inMemory{
		app:   make(map[string]string),
		users: make(map[string]map[string]string),
	}
}

func (i *inMemory) Get(_ context.Context, scope StateScope, key string) (string, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	switch scope {
	case ScopeApp:
		val, ok := i.app[key]
		if !ok {
			return "", errors.New("key not found")
		}
		return val, nil
	default:
		return "", errors.New("invalid scope: use WithUser to create user-scoped state")
	}
}

func (i *inMemory) Set(_ context.Context, scope StateScope, key string, value string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	switch scope {
	case ScopeApp:
		i.app[key] = value
		return nil
	default:
		return errors.New("invalid scope: use WithUser to create user-scoped state")
	}
}

func (i *inMemory) Delete(_ context.Context, scope StateScope, key string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	switch scope {
	case ScopeApp:
		delete(i.app, key)
		return nil
	default:
		return errors.New("invalid scope: use WithUser to create user-scoped state")
	}
}

func (i *inMemory) List(_ context.Context, scope StateScope) ([]string, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	switch scope {
	case ScopeApp:
		keys := make([]string, 0, len(i.app))
		for k := range i.app {
			keys = append(keys, k)
		}
		return keys, nil
	default:
		return nil, errors.New("invalid scope: use WithUser to create user-scoped state")
	}
}

func (i *inMemory) WithUser(userID string) State {
	return &userState{
		parent: i,
		userID: userID,
	}
}

type userState struct {
	parent *inMemory
	userID string
}

func (u *userState) Get(_ context.Context, scope StateScope, key string) (string, error) {
	u.parent.mu.RLock()
	defer u.parent.mu.RUnlock()

	userData, ok := u.parent.users[u.userID]
	if !ok {
		return "", errors.New("key not found")
	}
	val, ok := userData[key]
	if !ok {
		return "", errors.New("key not found")
	}
	return val, nil
}

func (u *userState) Set(_ context.Context, scope StateScope, key string, value string) error {
	u.parent.mu.Lock()
	defer u.parent.mu.Unlock()

	if _, ok := u.parent.users[u.userID]; !ok {
		u.parent.users[u.userID] = make(map[string]string)
	}
	u.parent.users[u.userID][key] = value
	return nil
}

func (u *userState) Delete(_ context.Context, scope StateScope, key string) error {
	u.parent.mu.Lock()
	defer u.parent.mu.Unlock()

	if userData, ok := u.parent.users[u.userID]; ok {
		delete(userData, key)
	}
	return nil
}

func (u *userState) List(_ context.Context, scope StateScope) ([]string, error) {
	u.parent.mu.RLock()
	defer u.parent.mu.RUnlock()

	userData, ok := u.parent.users[u.userID]
	if !ok {
		return []string{}, nil
	}
	keys := make([]string, 0, len(userData))
	for k := range userData {
		keys = append(keys, k)
	}
	return keys, nil
}

func (u *userState) WithUser(userID string) State {
	return u.parent.WithUser(userID)
}
