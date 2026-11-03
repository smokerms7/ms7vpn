package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ms7vpn/internal/model"
)

func TestTUNNeverSilentlyFallsBackWithoutAdmin(t *testing.T) {
	if IsAdministrator() {
		t.Skip("requires a non-elevated test process")
	}
	a := newTestApp(t)
	s := a.state.Settings
	s.ConnectionMode = "tun"
	a.SaveSettings(s)
	_, err := a.Connect(context.Background(), "", "tun")
	e, ok := err.(*APIError)
	if !ok || e.Code != "NEED_ADMIN" {
		t.Fatalf("expected NEED_ADMIN, got %v", err)
	}
	if a.Snapshot().Settings.ConnectionMode != "tun" {
		t.Fatal("silently changed requested TUN to proxy")
	}
}

func TestCoreSelectionMigration(t *testing.T) {
	for _, engine := range []string{"", "invalid", "xray", "sing-box"} {
		s := model.DefaultSettings()
		s.Engine = engine
		want := engine
		if want != "sing-box" {
			want = "xray"
		}
		if got := normalizeSettings(s).Engine; got != want {
			t.Fatalf("%q -> %q", engine, got)
		}
	}
}

func TestProxyPortsKeepExistingApplicationSettings(t *testing.T) {
	httpPort, socksPort := proxyPorts(func(preferred int) int { return preferred })
	if httpPort != 10809 || socksPort != 10808 {
		t.Fatalf("legacy HTTP/SOCKS endpoints changed: %d/%d", httpPort, socksPort)
	}
}

func TestProxyPortsAvoidCollisionWhenLegacySOCKSPortIsBusy(t *testing.T) {
	occupied := map[int]bool{10808: true}
	httpPort, socksPort := proxyPorts(func(preferred int) int {
		for occupied[preferred] {
			preferred++
		}
		return preferred
	})
	if httpPort != 10809 || socksPort != 10810 || httpPort == socksPort {
		t.Fatalf("occupied SOCKS port collided with HTTP: %d/%d", httpPort, socksPort)
	}
}

func TestCoreChangeDuringConnectionRejected(t *testing.T) {
	app := newTestApp(t)
	app.state.Connection.Connecting = true
	s := app.Snapshot().Settings
	s.Engine = "sing-box"
	if _, err := app.SaveSettings(s); err == nil {
		t.Fatal("native settings changed during connection")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"mode":"vpn","connectionMode":"system-proxy","engine":"sing-box"}`))
	rec := httptest.NewRecorder()
	app.handleSettings(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("HTTP status: %d", rec.Code)
	}
	if app.Snapshot().Settings.Engine != "xray" {
		t.Fatal("engine was changed")
	}
}
