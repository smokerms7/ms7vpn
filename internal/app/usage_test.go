package app

import (
	"testing"

	"ms7vpn/internal/model"
)

func TestUsageBookCountsDeltasAndPersists(t *testing.T) {
	dir := t.TempDir()
	book := loadUsage(dir)
	book.startSession()
	book.addTraffic(100, 1000)
	book.addTraffic(150, 1600)
	// Counter reset inside the core: the whole new value is growth.
	book.addTraffic(20, 30)
	book.addPing(40)
	book.addPing(60)
	book.maybeSave(true)

	reloaded := loadUsage(dir)
	days := reloaded.view(30)
	if len(days) != 30 {
		t.Fatalf("view length = %d", len(days))
	}
	today := days[len(days)-1]
	if today.Up != 170 || today.Down != 1630 || today.Connections != 1 || today.AvgPing != 50 {
		t.Fatalf("today = %+v", today)
	}
	total := reloaded.totals()
	if total.Up != 170 || total.Connections != 1 {
		t.Fatalf("totals = %+v", total)
	}
	reloaded.reset()
	if got := loadUsage(dir).totals(); got.Up != 0 || got.Connections != 0 {
		t.Fatalf("reset left data: %+v", got)
	}
}

func TestNormalizeSettingsCleansSplitLists(t *testing.T) {
	settings := model.DefaultSettings()
	settings.DirectDomains = []string{"https://www.Example.org/x", "example.org", "junk value"}
	settings.DirectApps = []string{`C:\Games\Game.EXE`}
	settings.ServerSort = "weird"
	got := normalizeSettings(settings)
	if len(got.DirectDomains) != 1 || got.DirectDomains[0] != "example.org" {
		t.Fatalf("domains = %#v", got.DirectDomains)
	}
	if len(got.DirectApps) != 1 || got.DirectApps[0] != "game.exe" {
		t.Fatalf("apps = %#v", got.DirectApps)
	}
	if got.ServerSort != model.SortPing {
		t.Fatalf("sort = %q", got.ServerSort)
	}
}

func TestTunnelKeyIgnoresNilVersusEmpty(t *testing.T) {
	a := model.DefaultSettings()
	b := a
	b.DirectDomains, b.ProxyDomains, b.DirectApps = nil, nil, nil
	if tunnelKey(a) != tunnelKey(b) {
		t.Fatal("nil and empty lists must not trigger a reconnect")
	}
	b.DirectRu = true
	if tunnelKey(a) == tunnelKey(b) {
		t.Fatal("DirectRu change must trigger a reconnect")
	}
}
