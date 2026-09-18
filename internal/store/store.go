package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ms7vpn/internal/model"
)

type Store struct {
	mu      sync.Mutex
	path    string
	dataDir string
}

func New(dataDir string) *Store {
	return &Store{path: filepath.Join(dataDir, "state.json"), dataDir: dataDir}
}

func (s *Store) DataDir() string { return s.dataDir }

func (s *Store) Load() (model.AppState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := model.DefaultState()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return state, fmt.Errorf("read state: %w", err)
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		backup := s.path + ".broken"
		_ = os.WriteFile(backup, data, 0o600)
		return model.DefaultState(), fmt.Errorf("state damaged; backup %s: %w", backup, err)
	}
	if state.Version == "" {
		state.Version = model.AppVersion
	}
	if state.Settings.ConnectionMode == "" {
		state.Settings = model.DefaultSettings()
	}
	if state.Settings.DNS1 == "" {
		state.Settings.DNS1 = "1.1.1.1"
	}
	if state.Settings.DNS2 == "" {
		state.Settings.DNS2 = "8.8.8.8"
	}
	if state.Settings.TestURL == "" {
		state.Settings.TestURL = "https://www.gstatic.com/generate_204"
	}
	if state.Settings.SupportURL == "" {
		state.Settings.SupportURL = "https://t.me/ms7support"
	}
	// Прежний адрес по умолчанию указывал на сайт, которого может не быть.
	// Переводим такие установки на GitHub; свой адрес пользователя не трогаем.
	if state.Settings.UpdateURL == "" || state.Settings.UpdateURL == "https://ms7pc.shop/app/latest.json" {
		state.Settings.UpdateURL = model.DefaultSettings().UpdateURL
	}
	if state.Favorites == nil {
		state.Favorites = map[string]bool{}
	}
	if state.Subscriptions == nil {
		state.Subscriptions = []model.Subscription{}
	}
	if state.RecentNodeIDs == nil {
		state.RecentNodeIDs = []string{}
	}
	state.Connection = model.ConnectionState{}
	state.Core.Downloading = false
	state.Core.Progress = 0
	state.Core.LastError = ""
	return state, nil
}

func (s *Store) Save(state model.AppState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	persisted := state
	persisted.Version = model.AppVersion
	persisted.Connection = model.ConnectionState{}
	persisted.Core.Downloading = false
	persisted.Core.Progress = 0
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}
