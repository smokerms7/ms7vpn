package store

import (
	"testing"

	"ms7vpn/internal/model"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	directory := t.TempDir()
	storage := New(directory)
	state := model.DefaultState()
	state.SelectedNodeID = "node-1"
	state.Subscriptions = append(state.Subscriptions, model.Subscription{ID: "sub-1", Name: "MS7"})
	if err := storage.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := storage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SelectedNodeID != "node-1" || len(loaded.Subscriptions) != 1 {
		t.Fatalf("unexpected state: %+v", loaded)
	}
	if loaded.Favorites == nil {
		t.Fatal("favorites map must be initialized")
	}
}
