package store

import (
	"context"
	"encoding/json"
	"northstar/backend/internal/domain"
	"sync"
)

type Store interface {
	Read(context.Context) (domain.State, error)
	Update(context.Context, func(*domain.State) error) error
	Close()
}
type Memory struct {
	mu    sync.RWMutex
	state domain.State
}

func NewMemory(state domain.State) *Memory { domain.Normalize(&state); return &Memory{state: state} }
func clone(state domain.State) domain.State {
	data, _ := json.Marshal(state)
	var copy domain.State
	_ = json.Unmarshal(data, &copy)
	for i := range copy.Employees {
		copy.Employees[i].PasswordHash = state.Employees[i].PasswordHash
	}
	copy.Sessions = append([]domain.Session{}, state.Sessions...)
	domain.ClonePrivate(state, &copy)
	return copy
}
func (m *Memory) Read(ctx context.Context) (domain.State, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return domain.Scoped(clone(m.state), domain.Tenant(ctx)), ctx.Err()
}
func (m *Memory) Update(ctx context.Context, fn func(*domain.State) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := domain.Scoped(clone(m.state), domain.Tenant(ctx))
	if err := fn(&next); err != nil {
		return err
	}
	if err := domain.MergeTenant(&m.state, next, domain.Tenant(ctx)); err != nil {
		return err
	}
	return nil
}
func (m *Memory) Close() {}
