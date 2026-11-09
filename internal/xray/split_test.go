package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSplitRoutingRulesOrderAndTunDirect(t *testing.T) {
	raw, err := BuildConfig(mustNode(t), ConfigOptions{
		Mode: "system-proxy", BindInterface: "Ethernet", ServerIP: "192.0.2.9",
		DirectRu: true, DirectDomains: []string{"kino.example"}, ProxyDomains: []string{"vk.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	proxyAt := strings.Index(text, `"domain:vk.com"`)
	userDirectAt := strings.Index(text, `"domain:kino.example"`)
	ruAt := strings.Index(text, `"domain:xn--p1ai"`)
	if proxyAt < 0 || userDirectAt < 0 || ruAt < 0 {
		t.Fatalf("rules missing:\n%s", text)
	}
	if !(proxyAt < userDirectAt && userDirectAt < ruAt) {
		t.Fatalf("wrong order: proxy=%d userDirect=%d ru=%d", proxyAt, userDirectAt, ruAt)
	}
	if strings.Contains(text, "geoip:ru") {
		t.Fatal("geoip:ru must not be used without geoip.dat")
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	direct := config["outbounds"].([]any)[1].(map[string]any)
	sockopt := direct["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	if sockopt["interface"] != "Ethernet" {
		t.Fatalf("direct outbound must bypass TUN: %+v", direct)
	}
}

func TestNoSplitRulesByDefault(t *testing.T) {
	raw, err := BuildConfig(mustNode(t), ConfigOptions{Mode: "system-proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "domain:ru") || strings.Contains(string(raw), "geoip:ru") {
		t.Fatal("split rules leaked into default config")
	}
}
