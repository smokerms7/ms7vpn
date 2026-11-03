package app

import (
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

var errVPNConflictNeedsAdmin = errors.New("для отключения другого VPN нужны права администратора")

// Exact client/core names only: never stop browsers, Telegram, Steam, ssh,
// Windows networking services, or processes merely containing "vpn".
var vpnClients = map[string]bool{
	"happ.exe": true, "v2rayn.exe": true,
	"nekoray.exe": true, "nekobox.exe": true, "hiddify.exe": true,
	"clash-verge.exe": true, "clash for windows.exe": true,
}
var vpnCores = map[string]bool{
	"happd.exe": true,
	"xray.exe":  true, "sing-box.exe": true, "mihomo.exe": true,
	"clash.exe": true, "clash-meta.exe": true, "tun2socks.exe": true,
	"hysteria.exe": true, "hysteria2.exe": true,
}

func conflictingVPNProcess(name, path string, ownPaths []string) bool {
	name = strings.ToLower(name)
	if !vpnClients[name] && !vpnCores[name] {
		return false
	}
	base := path[strings.LastIndexAny(path, `/\`)+1:]
	if !strings.EqualFold(base, name) {
		return false
	}
	for _, own := range ownPaths {
		if strings.EqualFold(filepath.Clean(path), filepath.Clean(own)) {
			return false
		}
	}
	return true
}

// Match only endpoints actually listened on by processes being stopped. A
// remote corporate proxy or an unrelated local development proxy is untouched.
func proxyUsesStoppedPort(server string, ports map[int]bool) bool {
	for _, part := range strings.Split(server, ";") {
		part = strings.TrimSpace(part)
		if i := strings.IndexByte(part, '='); i >= 0 {
			part = part[i+1:]
		}
		host, port, err := net.SplitHostPort(part)
		if err != nil {
			continue
		}
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			continue
		}
		n, err := strconv.Atoi(port)
		if err == nil && ports[n] {
			return true
		}
	}
	return false
}
