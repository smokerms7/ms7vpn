package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// Проверяем самодельный gRPC-клиент на настоящем ядре Xray.
// Тест выполняется, только если путь к ядру задан в MS7_XRAY.
func TestStatsAgainstRealCore(t *testing.T) {
	binary := os.Getenv("MS7_XRAY")
	if binary == "" {
		t.Skip("MS7_XRAY не задан")
	}
	dir := t.TempDir()
	apiPort := FreeLocalPort(18085)
	socksPort := FreeLocalPort(18080)

	config := map[string]any{
		"log":    map[string]any{"loglevel": "warning"},
		"api":    map[string]any{"tag": "api", "listen": "127.0.0.1:" + itoa(apiPort), "services": []string{"StatsService"}},
		"stats":  map[string]any{},
		"policy": map[string]any{"system": map[string]any{"statsOutboundUplink": true, "statsOutboundDownlink": true}},
		"inbounds": []any{map[string]any{
			"tag": "socks-in", "listen": "127.0.0.1", "port": socksPort, "protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": false},
		}},
		"outbounds": []any{
			map[string]any{"tag": "proxy", "protocol": "freedom"},
			map[string]any{"tag": "direct", "protocol": "freedom"},
		},
		"routing": map[string]any{"domainStrategy": "AsIs", "rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"},
		}},
	}
	raw, _ := json.MarshalIndent(config, "", "  ")
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(binary, "run", "-c", path)
	command.Dir = filepath.Dir(binary)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _, _ = command.Process.Wait() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := WaitLocalPort(ctx, apiPort, 8*time.Second); err != nil {
		t.Fatalf("API не открылся: %v", err)
	}

	client := NewStatsClient(apiPort)
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if _, err = client.Query(ctx); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("запрос статистики не прошёл: %v", err)
	}

	// Прогоняем настоящий трафик через SOCKS-вход и проверяем, что счётчики
	// выросли, а скорость посчиталась.
	payload := bytes.Repeat([]byte("ms7"), 40000) // ~120 КБ
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:"+itoa(socksPort), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{
		DisableKeepAlives: true, // счётчики Xray закрывают учёт вместе с соединением
		DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	for i := 0; i < 3; i++ {
		response, reqErr := httpClient.Get(server.URL)
		if reqErr != nil {
			t.Fatalf("запрос через SOCKS не прошёл: %v", reqErr)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}

	transport.CloseIdleConnections()
	time.Sleep(1500 * time.Millisecond)
	traffic, err := client.Query(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("после трафика: вниз %d байт, вверх %d байт, скорость вниз %d бит/с",
		traffic.DownlinkBytes, traffic.UplinkBytes, traffic.DownlinkBitsSec)
	if traffic.DownlinkBytes < int64(len(payload)) {
		t.Fatalf("счётчик приёма не вырос: %d байт", traffic.DownlinkBytes)
	}
	if traffic.UplinkBytes <= 0 {
		t.Fatal("счётчик отдачи остался нулевым")
	}
}

func itoa(value int) string {
	digits := ""
	if value == 0 {
		return "0"
	}
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
