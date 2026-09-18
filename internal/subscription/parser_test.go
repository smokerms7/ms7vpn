package subscription

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

const testUUID = "11111111-2222-3333-4444-555555555555"

func TestParseSupportedShareLinks(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"NL VMess","add":"nl.example.com","port":"443","id":"` + testUUID + `","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"cdn.example.com"}`
	vmess := "vmess://" + base64.RawStdEncoding.EncodeToString([]byte(vmessJSON))
	ssCreds := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret"))

	cases := []struct {
		name      string
		uri       string
		protocol  string
		transport string
		security  string
	}{
		{
			name:     "vless raw reality",
			uri:      "vless://" + testUUID + "@de.example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=public-key&sid=a1b2c3&type=tcp#Germany",
			protocol: "vless", transport: "raw", security: "reality",
		},
		{
			name:     "vless xhttp tls",
			uri:      "vless://" + testUUID + "@be.example.com:443?encryption=none&security=tls&sni=cdn.example.com&type=xhttp&host=cdn.example.com&path=%2Fms7&mode=auto#Bypass",
			protocol: "vless", transport: "xhttp", security: "tls",
		},
		{
			name: "vmess ws tls", uri: vmess,
			protocol: "vmess", transport: "ws", security: "tls",
		},
		{
			name:     "trojan grpc tls",
			uri:      "trojan://strong-password@tr.example.com:443?security=tls&sni=tr.example.com&type=grpc&serviceName=ms7#Turkey",
			protocol: "trojan", transport: "grpc", security: "tls",
		},
		{
			name: "shadowsocks", uri: "ss://" + ssCreds + "@ss.example.com:8388#SS",
			protocol: "shadowsocks", transport: "raw", security: "none",
		},
		{
			name: "socks", uri: "socks://user:pass@socks.example.com:1080#SOCKS",
			protocol: "socks", transport: "raw", security: "none",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node, _, err := ParseURI(tc.uri)
			if err != nil {
				t.Fatalf("ParseURI: %v", err)
			}
			if node.Protocol != tc.protocol || node.Transport != tc.transport || node.Security != tc.security {
				t.Fatalf("got %s/%s/%s", node.Protocol, node.Transport, node.Security)
			}
			if !node.Supported {
				t.Fatalf("expected supported, reason: %s", node.UnsupportedReason)
			}
		})
	}
}

func TestParseBase64SubscriptionAndDeduplicate(t *testing.T) {
	link := "vless://" + testUUID + "@de.example.com:443?encryption=none&security=tls&type=tcp#One"
	body := link + "\n" + link + "\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	nodes, warnings, err := ParseContent([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
}

func TestFullXrayJSONRejectedWithoutSilentMutation(t *testing.T) {
	_, _, err := ParseContent([]byte(`{"inbounds":[],"outbounds":[],"routing":{}}`))
	if err == nil || !strings.Contains(err.Error(), "полный Xray JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetcherHeadersAndContent(t *testing.T) {
	link := "vless://" + testUUID + "@de.example.com:443?encryption=none&security=tls&type=tcp#One"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Запрос за сведениями о подписке идёт отдельно и от имени браузера —
		// проверки ниже относятся только к загрузке списка серверов.
		if r.Header.Get("X-MS7-Info") != "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "Happ/") {
			t.Fatalf("unexpected User-Agent: %q", r.Header.Get("User-Agent"))
		}
		if len(r.Header.Get("X-Hwid")) < 10 {
			t.Fatalf("missing X-Hwid: %q", r.Header.Get("X-Hwid"))
		}
		if r.Header.Get("X-Device-Os") != "Windows" {
			t.Fatalf("unexpected X-Device-Os: %q", r.Header.Get("X-Device-Os"))
		}
		w.Header().Set("profile-title", base64.StdEncoding.EncodeToString([]byte("MS7 Test")))
		w.Header().Set("subscription-userinfo", "upload=1024; download=2048; total=107374182400; expire=1893456000")
		w.Header().Set("profile-update-interval", "6")
		_, _ = fmt.Fprint(w, link)
	}))
	defer server.Close()

	result, err := NewFetcher(t.TempDir()).Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "MS7 Test" || result.UpdateIntervalMinutes != 360 || len(result.Nodes) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.UserInfo.TotalBytes != 107374182400 {
		t.Fatalf("unexpected user info: %+v", result.UserInfo)
	}
}

func TestProfileTitleBase64Prefix(t *testing.T) {
	if got := ParseProfileTitle("base64:TVM3IFZQTg=="); got != "MS7 VPN" {
		t.Fatalf("ожидали MS7 VPN, получили %q", got)
	}
	if got := ParseProfileTitle("MS7 VPN"); got != "MS7 VPN" {
		t.Fatalf("простой заголовок сломан: %q", got)
	}
}

// Название подписки раньше портилось: обычное имя вроде «MS7VPN» проходит как
// валидный base64 и декодировалось в бинарный мусор.
func TestParseProfileTitleKeepsPlainNames(t *testing.T) {
	cases := map[string]string{
		"MS7VPN":       "MS7VPN",
		"MS7 VPN":      "MS7 VPN",
		"Подписка":     "Подписка",
		"base64:0JzQodep": "",  // битый base64 — остаётся как есть, но без мусора
	}
	for input, want := range cases {
		got := ParseProfileTitle(input)
		if want != "" && got != want {
			t.Fatalf("ParseProfileTitle(%q) = %q, ожидалось %q", input, got, want)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("ParseProfileTitle(%q) вернул не-UTF8: %q", input, got)
		}
	}
}

func TestParseProfileTitleDecodesRealBase64(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("MS7 VPN Премиум"))
	if got := ParseProfileTitle("base64:" + encoded); got != "MS7 VPN Премиум" {
		t.Fatalf("got %q", got)
	}
	if got := ParseProfileTitle(encoded); got != "MS7 VPN Премиум" {
		t.Fatalf("got %q", got)
	}
}

func TestHysteria2MarkedUnsupported(t *testing.T) {
	node, _, err := ParseURI("hy2://hy-auth@hy.example.com:443?sni=hy.example.com#HY2")
	if err != nil {
		t.Fatal(err)
	}
	if node.Supported {
		t.Fatal("hysteria2 не должен быть поддерживаемым: в Xray-core такого протокола нет")
	}
}
