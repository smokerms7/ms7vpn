package singbox

import (
	"strings"
	"testing"

	"ms7vpn/internal/subscription"
)

func TestSingBoxSplitRules(t *testing.T) {
	node, _, err := subscription.ParseURI("vless://" + testUUID + "@192.0.2.1:443?type=tcp")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Build(node, Options{Mode: "tun", APIPort: 9090, Secret: "x", DirectRu: true, DirectDomains: []string{"kino.example"}, ProxyDomains: []string{"vk.com"}, DirectApps: []string{"steam.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"process_name"`, `"steam.exe"`, `"kino.example"`, `"xn--p1ai"`, `"vk.com"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in\n%s", want, text)
		}
	}
	// With Xray holding the connection only process rules stay in sing-box.
	raw, err = Build(node, Options{Mode: "tun", APIPort: 9090, Secret: "x", XraySOCKSPort: 10808, DirectRu: true, DirectApps: []string{"steam.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	text = string(raw)
	if !strings.Contains(text, `"process_name"`) {
		t.Fatal("process rule missing for xray engine")
	}
	if strings.Contains(text, `"outbound": "direct"`) && strings.Count(text, `"outbound": "direct"`) != 2 {
		t.Fatalf("unexpected domain route rules with xray engine:\n%s", text)
	}
}
