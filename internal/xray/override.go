package xray

import "os"

// Переопределения для мобильной сборки (Android). На Windows не задаются.
var (
	// CoreBinaryOverride — путь к исполняемому файлу Xray (на Android —
	// libxray.so из папки нативных библиотек приложения).
	CoreBinaryOverride string
	// CoreDirOverride — папка с geoip.dat и geosite.dat.
	CoreDirOverride string
	// TunMTU — MTU входа tun. Windows: 9000; Android: как у VpnService.
	TunMTU = 9000
	// NoSniffing — не определять домены в трафике (sniffing).
	NoSniffing bool
	// InboundListen — адрес локальных SOCKS/HTTP: 0.0.0.0 открывает их сети.
	InboundListen = "127.0.0.1"
	// Fragment — дробить TLS ClientHello; Mux — мультиплексирование;
	// DomainStrategy — стратегия маршрутизации (пусто — IPIfNonMatch).
	Fragment       bool
	Mux            bool
	DomainStrategy string
)

func sniffing(dest ...string) map[string]any {
	return map[string]any{"enabled": !NoSniffing, "destOverride": dest, "routeOnly": true}
}

// SetTunFile задаёт дескриптор TUN для следующего запуска в режиме tun.
// nil — снять. Файл закрывает вызывающий.
func (m *Manager) SetTunFile(file *os.File) {
	m.mu.Lock()
	m.tunFile = file
	m.mu.Unlock()
}

// applyTransportExtras добавляет фрагментацию и mux к исходящему прокси.
// Mux не ставится при flow (XTLS Vision) и на XHTTP — они с ним несовместимы.
func applyTransportExtras(outbounds []any) []any {
	proxy, ok := outbounds[0].(map[string]any)
	if !ok {
		return outbounds
	}
	stream, _ := proxy["streamSettings"].(map[string]any)
	if Fragment {
		if stream == nil {
			stream = map[string]any{}
			proxy["streamSettings"] = stream
		}
		sockopt, _ := stream["sockopt"].(map[string]any)
		if sockopt == nil {
			sockopt = map[string]any{}
			stream["sockopt"] = sockopt
		}
		sockopt["dialerProxy"] = "fragment"
		outbounds = append(outbounds, map[string]any{
			"tag": "fragment", "protocol": "freedom",
			"settings": map[string]any{"domainStrategy": "UseIP", "fragment": map[string]any{"packets": "tlshello", "length": "100-200", "interval": "10-20"}},
		})
	}
	if Mux && !hasFlow(proxy) && (stream == nil || stream["network"] != "xhttp") {
		proxy["mux"] = map[string]any{"enabled": true, "concurrency": 8, "xudpConcurrency": 16, "xudpProxyUDP443": "reject"}
	}
	return outbounds
}

func hasFlow(proxy map[string]any) bool {
	settings, _ := proxy["settings"].(map[string]any)
	vnext, _ := settings["vnext"].([]any)
	for _, v := range vnext {
		server, _ := v.(map[string]any)
		users, _ := server["users"].([]any)
		for _, u := range users {
			if user, _ := u.(map[string]any); user != nil {
				if flow, _ := user["flow"].(string); flow != "" {
					return true
				}
			}
		}
	}
	return false
}

func routingStrategy() string {
	switch DomainStrategy {
	case "AsIs", "IPIfNonMatch", "IPOnDemand":
		return DomainStrategy
	}
	return "IPIfNonMatch"
}
