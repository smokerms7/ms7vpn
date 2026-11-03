package app

import "testing"

func TestTakeoverOnlyStopsKnownVPNsAndPreservesOwnCores(t *testing.T) {
	own := []string{`C:\MS7VPN\xray.exe`, `C:\MS7VPN\sing-box.exe`}
	for _, c := range []struct {
		name, path string
		want       bool
	}{
		{"Happ.exe", `C:\Program Files\Happ\Happ.exe`, true},
		{"xray.exe", `C:\Program Files\Happ\core\xray.exe`, true},
		{"sing-box.exe", `C:\OtherClient\sing-box.exe`, true},
		{"xray.exe", `C:\MS7VPN\xray.exe`, false},
		{"xray.exe", `C:\Other\browser.exe`, false},
		{"SING-BOX.EXE", `c:\ms7vpn\sing-box.exe`, false},
		{"MS7VPN.exe", `C:\MS7VPN\MS7VPN.exe`, false},
		{"Telegram.exe", `C:\Telegram\Telegram.exe`, false},
		{"Discord.exe", `C:\Discord\Discord.exe`, false},
		{"steam.exe", `C:\Steam\steam.exe`, false},
		{"ssh.exe", `C:\Windows\ssh.exe`, false},
		{"svchost.exe", `C:\Windows\svchost.exe`, false},
		{"some-vpn.exe", `C:\Tools\some-vpn.exe`, false},
	} {
		if got := conflictingVPNProcess(c.name, c.path, own); got != c.want {
			t.Errorf("%s %s: %v", c.name, c.path, got)
		}
	}
}

func TestProxyCleanupOnlyMatchesPortsOfStoppedProcesses(t *testing.T) {
	ports := map[int]bool{10808: true, 10809: true}
	for _, c := range []struct {
		server string
		want   bool
	}{
		{"127.0.0.1:10809", true},
		{"http=127.0.0.1:10809;https=127.0.0.1:10809;socks=127.0.0.1:10808", true},
		{"socks=[::1]:10808", true},
		{"localhost:10809", true},
		{"127.0.0.1:8080", false},
		{"proxy.example.com:10809", false},
		{"192.168.0.1:10809", false},
		{"", false},
	} {
		if got := proxyUsesStoppedPort(c.server, ports); got != c.want {
			t.Errorf("%s: %v", c.server, got)
		}
	}
}
