package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeTunConfig(t *testing.T) {
	raw, err := BuildConfig(mustNode(t), ConfigOptions{Mode: "tun", NativeTun: true, TunName: "MS7VPN-XRAY-2", SendThrough: "192.168.0.105", ServerIP: "192.0.2.9", DirectApps: []string{"Steam.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	tun := cfg["inbounds"].([]any)[0].(map[string]any)
	if tun["protocol"] != "tun" || tun["settings"].(map[string]any)["name"] != "MS7VPN-XRAY-2" {
		t.Fatalf("tun inbound: %+v", tun)
	}
	text := string(raw)
	for _, want := range []string{`"sendThrough": "192.168.0.105"`, `"dns-out"`, `"steam.exe"`, `"Steam.exe"`, `"192.0.2.9"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if _, err := BuildConfig(mustNode(t), ConfigOptions{Mode: "tun"}); err == nil {
		t.Fatal("plain tun without NativeTun must still be rejected")
	}
}
