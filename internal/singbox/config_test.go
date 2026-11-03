package singbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ms7vpn/internal/subscription"
	"ms7vpn/internal/xray"
)

func TestRealCoreStartsAndAuthenticatesStats(t *testing.T) {
	core := os.Getenv("MS7_SINGBOX")
	if core == "" {
		t.Skip("MS7_SINGBOX not set")
	}
	node, _, err := subscription.ParseURI("vless://" + testUUID + "@192.0.2.1:443?type=tcp")
	if err != nil {
		t.Fatal(err)
	}
	httpPort := xray.FreeLocalPort(18080)
	socksPort := xray.FreeLocalPort(httpPort + 1)
	apiPort := xray.FreeLocalPort(socksPort + 1)
	raw, err := Build(node, Options{Mode: "system-proxy", HTTPPort: httpPort, SOCKSPort: socksPort, APIPort: apiPort, Secret: "test-only"})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(t.TempDir(), nil)
	m.path = core
	t.Cleanup(func() { m.Stop() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Start(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitAlive(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStatsClient(apiPort, "wrong-secret").Query(ctx); err == nil {
		t.Fatal("unauthenticated stats access")
	}
	if _, err := NewStatsClient(apiPort, "test-only").Query(ctx); err != nil {
		t.Fatal(err)
	}
}

const testUUID = "11111111-2222-3333-4444-555555555555"
const testKey = "bG82oozlUxc0XmFsub86K9vItGcBFt4iWy0cHqKsyks"

func TestConfigurationsAgainstRealCore(t *testing.T) {
	cases := map[string]string{
		"vless-reality":     "vless://" + testUUID + "@192.0.2.1:443?security=reality&type=tcp&sni=example.org&pbk=" + testKey + "&sid=a1b2&fp=chrome&flow=xtls-rprx-vision",
		"vless-ws":          "vless://" + testUUID + "@example.org:443?security=tls&type=ws&host=example.org&path=%2Fws",
		"vless-grpc":        "vless://" + testUUID + "@example.org:443?security=tls&type=grpc&serviceName=vpn",
		"vless-httpupgrade": "vless://" + testUUID + "@example.org:443?security=tls&type=httpupgrade&host=example.org&path=%2Fvpn",
		"trojan":            "trojan://password@example.org:443?security=tls&sni=example.org",
		"shadowsocks":       "ss://YWVzLTEyOC1nY206cGFzc3dvcmQ@example.org:8388",
		"socks":             "socks://user:pass@192.0.2.1:1080",
		"hysteria2":         "hy2://password@example.org:443?sni=example.org&obfs=salamander&obfs-password=obfs-password",
	}
	core := os.Getenv("MS7_SINGBOX")
	for name, uri := range cases {
		for _, mode := range []string{"system-proxy", "tun"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				node, _, err := subscription.ParseURI(uri)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := Build(node, Options{Mode: mode, HTTPPort: 10809, SOCKSPort: 10808, APIPort: 10090, Secret: "test-only", Interface: "Ethernet", BlockAds: true})
				if err != nil {
					t.Fatal(err)
				}
				if core == "" {
					return
				}
				path := filepath.Join(t.TempDir(), "config.json")
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(core, "check", "-c", path)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v: %s", err, output)
				}
			})
		}
	}
}

func TestXrayBridgeUsesManagedTUN(t *testing.T) {
	node, _, _ := subscription.ParseURI("vless://" + testUUID + "@example.org:443?type=xhttp&security=tls")
	raw, err := Build(node, Options{Mode: "tun", APIPort: 10090, Secret: "test-only", Interface: "Ethernet", XraySOCKSPort: 10808})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	tun := config["inbounds"].([]any)[0].(map[string]any)
	if tun["auto_route"] != true || tun["strict_route"] != true || tun["dns_mode"] != "hijack" {
		t.Fatalf("unsafe TUN: %s", raw)
	}
	route := config["route"].(map[string]any)
	if route["default_interface"] != "Ethernet" {
		t.Fatal("missing loop prevention")
	}
	proxy := config["outbounds"].([]any)[0].(map[string]any)
	if proxy["type"] != "socks" || proxy["server_port"] != float64(10808) {
		t.Fatal("Xray engine was bypassed")
	}
	if core := os.Getenv("MS7_SINGBOX"); core != "" {
		path := filepath.Join(t.TempDir(), "bridge.json")
		os.WriteFile(path, raw, 0600)
		if out, err := exec.Command(core, "check", "-c", path).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
}

func TestIncompatibleXrayTransportRejected(t *testing.T) {
	for _, transport := range []string{"xhttp", "kcp"} {
		node, _, _ := subscription.ParseURI("vless://" + testUUID + "@example.org:443?type=" + transport)
		if _, err := Outbound(node); err == nil || !strings.Contains(err.Error(), "Xray") {
			t.Fatalf("%s: %v", transport, err)
		}
	}
}

func TestInvalidDNSRejected(t *testing.T) {
	node, _, _ := subscription.ParseURI("socks://192.0.2.1:1080")
	if _, err := Build(node, Options{Mode: "tun", DNS1: "https://invalid"}); err == nil {
		t.Fatal("invalid DNS accepted")
	}
}
