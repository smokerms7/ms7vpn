//go:build !windows

package zapret

import (
	"context"
	"errors"
	"time"
)

var errOnlyWindows = errors.New("режим «Запрет» работает только в Windows")

type session struct{ started time.Time }

func (m *Manager) Start(context.Context, Strategy) error { return errOnlyWindows }

func (m *Manager) Stop() error { return nil }

func (m *Manager) Cleanup() {}

func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running != nil
}

func (m *Manager) Uptime() time.Duration { return 0 }

func (m *Manager) StartedAt() time.Time { return time.Time{} }
