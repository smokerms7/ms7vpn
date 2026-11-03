package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"ms7vpn/internal/model"
	"ms7vpn/internal/subscription"
)

const uuid = "11111111-2222-3333-4444-555555555555"

func configFor(t *testing.T, uri, mode string) map[string]any {
	t.Helper()
	node, _, err := subscription.ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	content, err := BuildConfig(node, ConfigOptions{Mode: mode, HTTPPort: 10809, SOCKSPort: 10808, DNS1: "1.1.1.1", DNS2: "8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func proxyOutbound(config map[string]any) map[string]any {
	return config["outbounds"].([]any)[0].(map[string]any)
}

func mustNode(t *testing.T) model.Node {
	t.Helper()
	node, _, err := subscription.ParseURI("vless://" + uuid + "@de.example.com:443?encryption=none&security=reality&sni=www.microsoft.com&pbk=public-key&type=tcp#DE")
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestVLESSRealityConfig(t *testing.T) {
	config := configFor(t, "vless://"+uuid+"@de.example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=public-key&sid=a1b2&type=tcp#DE", "system-proxy")
	outbound := proxyOutbound(config)
	if outbound["protocol"] != "vless" {
		t.Fatalf("protocol: %v", outbound["protocol"])
	}
	stream := outbound["streamSettings"].(map[string]any)
	if stream["network"] != "raw" || stream["security"] != "reality" {
		t.Fatalf("stream: %+v", stream)
	}
	if _, ok := stream["realitySettings"].(map[string]any); !ok {
		t.Fatalf("realitySettings missing")
	}
}

func TestXrayTUNBackendPinsServerAndPhysicalInterface(t *testing.T) {
	raw, err := BuildConfig(mustNode(t), ConfigOptions{Mode: "system-proxy", BindInterface: "Ethernet", ServerIP: "192.0.2.9"})
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	json.Unmarshal(raw, &c)
	out := proxyOutbound(c)
	if got := out["streamSettings"].(map[string]any)["sockopt"].(map[string]any)["interface"]; got != "Ethernet" {
		t.Fatal("Xray sockets can loop into TUN")
	}
	server := out["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)
	if server["address"] != "192.0.2.9" {
		t.Fatal("server DNS can recurse through TUN")
	}
	if _, err := BuildConfig(mustNode(t), ConfigOptions{Mode: "tun"}); err == nil {
		t.Fatal("unsupported standalone Xray TUN accepted")
	}
}

func TestXHTTPProxyConfig(t *testing.T) {
	config := configFor(t, "vless://"+uuid+"@be.example.com:443?encryption=none&security=tls&sni=be.example.com&type=xhttp&host=be.example.com&path=%2Fms7&mode=auto#BE", "system-proxy")
	outbound := proxyOutbound(config)
	stream := outbound["streamSettings"].(map[string]any)
	if stream["network"] != "xhttp" {
		t.Fatalf("network: %v", stream["network"])
	}
	if _, ok := stream["xhttpSettings"].(map[string]any); !ok {
		t.Fatal("xhttpSettings missing")
	}

}

// Hysteria2 imports for sing-box but must never be passed to Xray.
func TestHysteria2RejectedUpFront(t *testing.T) {
	node, _, err := subscription.ParseURI("hy2://hy-auth@hy.example.com:443?sni=hy.example.com#HY2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildConfig(node, ConfigOptions{Mode: "system-proxy"}); err == nil {
		t.Fatal("BuildConfig должен отказать")
	}
}

// Конфиг не должен ссылаться на geoip/geosite, пока не включена блокировка
// рекламы: иначе Xray читает 30 МБ geo-файлов на каждом запуске.
func TestNoGeoFilesInDefaultConfig(t *testing.T) {
	for _, mode := range []string{"system-proxy"} {
		raw, err := BuildConfig(mustNode(t), ConfigOptions{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "geoip:") || strings.Contains(string(raw), "geosite:") {
			t.Fatalf("%s: конфиг ссылается на geo-файлы:\n%s", mode, raw)
		}
	}
}

// Блокировка рекламы требует geosite.dat. Если файла нет, правило нужно
// пропустить с предупреждением, а не отдавать Xray заведомо битый конфиг.
func TestBlockAdsSkippedWithoutGeosite(t *testing.T) {
	result, err := Build(mustNode(t), ConfigOptions{Mode: "system-proxy", BlockAds: true, GeoDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Config), "geosite:") {
		t.Fatal("правило добавлено без geosite.dat")
	}
	if len(result.Warnings) == 0 {
		t.Fatal("нет предупреждения об отсутствии geosite.dat")
	}
}

func TestOldQUICTransportRejected(t *testing.T) {
	node, _, err := subscription.ParseURI("vless://" + uuid + "@example.com:443?encryption=none&security=tls&type=quic#Old")
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildConfig(node, ConfigOptions{Mode: "system-proxy"})
	if err == nil || !strings.Contains(err.Error(), "QUIC") {
		t.Fatalf("unexpected error: %v", err)
	}
}
