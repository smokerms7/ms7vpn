package xray

import (
	"os"
	"path/filepath"
	"testing"

	"ms7vpn/internal/subscription"
)

func TestWriteConfigsForValidation(t *testing.T) {
	cases := map[string]string{
		"vless-reality": "vless://52762c90-974d-444e-9dd7-ddfb74b5bb41@151.242.161.153:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=example.org&fp=chrome&pbk=bG82oozlUxc0XmFsub86K9vItGcBFt4iWy0cHqKsyks&sid=9e5a335de26d1f5a&type=tcp#NL",
		"vless-ws-tls":  "vless://52762c90-974d-444e-9dd7-ddfb74b5bb41@a.example.com:443?encryption=none&security=tls&sni=a.example.com&type=ws&host=a.example.com&path=%2Fws#WS",
		"vless-xhttp":   "vless://52762c90-974d-444e-9dd7-ddfb74b5bb41@b.example.com:443?encryption=none&security=tls&sni=b.example.com&type=xhttp&host=b.example.com&path=%2Fx&mode=auto#XH",
		"vmess-ws":      "vmess://eyJ2IjoiMiIsInBzIjoiVk0iLCJhZGQiOiJjLmV4YW1wbGUuY29tIiwicG9ydCI6IjQ0MyIsImlkIjoiNTI3NjJjOTAtOTc0ZC00NDRlLTlkZDctZGRmYjc0YjViYjQxIiwiYWlkIjoiMCIsInNjeSI6ImF1dG8iLCJuZXQiOiJ3cyIsInR5cGUiOiJub25lIiwiaG9zdCI6ImMuZXhhbXBsZS5jb20iLCJwYXRoIjoiL3ciLCJ0bHMiOiJ0bHMifQ",
		"trojan-grpc":   "trojan://pass-word@d.example.com:443?security=tls&sni=d.example.com&type=grpc&serviceName=ms7#TJ",
		"shadowsocks":   "ss://YWVzLTI1Ni1nY206c2VjcmV0@e.example.com:8388#SS",
	}
	dir := os.Getenv("MS7_CONFIG_OUT")
	if dir == "" {
		t.Skip("MS7_CONFIG_OUT не задан")
	}
	for name, uri := range cases {
		node, _, err := subscription.ParseURI(uri)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, mode := range []string{"system-proxy", "tun"} {
			raw, err := BuildConfig(node, ConfigOptions{
				Mode: mode, HTTPPort: 10809, SOCKSPort: 10808,
				DNS1: "1.1.1.1", DNS2: "8.8.8.8", EnableStats: true, TunName: "MS7VPN",
			})
			if err != nil {
				t.Fatalf("%s/%s: %v", name, mode, err)
			}
			path := filepath.Join(dir, name+"."+mode+".json")
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
