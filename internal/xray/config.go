package xray

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ms7vpn/internal/model"
	"ms7vpn/internal/subscription"
)

// APIPort — порт локального Xray API (StatsService). Используется только на
// 127.0.0.1 и только для чтения счётчиков трафика.
const APIPort = 10085

// privateRanges — адреса, которые никогда не должны уходить в туннель.
// Раньше здесь стояло geoip:private, из-за чего Xray при каждом старте читал
// geoip.dat (19 МБ). Явный список даёт тот же результат мгновенно.
var privateRanges = []string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"100.64.0.0/10",
	"224.0.0.0/4",
	"255.255.255.255/32",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
}

// directDomains — то, что раньше покрывалось geosite:private.
var directDomains = []string{
	"localhost",
	"domain:local",
	"domain:localdomain",
	"domain:lan",
	"domain:home.arpa",
}

type ConfigOptions struct {
	Mode      string
	HTTPPort  int
	SOCKSPort int
	APIPort   int
	DNS1      string
	DNS2      string
	BlockAds  bool
	AccessLog string
	ErrorLog  string
	TunName   string

	// GeoDir — папка, где лежат geosite.dat/geoip.dat. Нужна только для
	// блокировки рекламы; если файлов нет, правило не добавляется, иначе
	// Xray откажется стартовать целиком.
	GeoDir string

	// EnableStats включает локальный API со счётчиками трафика.
	EnableStats bool
}

// BuildResult — конфиг плюс предупреждения, которые стоит показать человеку.
type BuildResult struct {
	Config   []byte
	Warnings []string
}

func BuildConfig(node model.Node, options ConfigOptions) ([]byte, error) {
	result, err := Build(node, options)
	if err != nil {
		return nil, err
	}
	return result.Config, nil
}

func Build(node model.Node, options ConfigOptions) (BuildResult, error) {
	var warnings []string
	if !node.Supported {
		reason := node.UnsupportedReason
		if reason == "" {
			reason = "профиль не поддерживается"
		}
		return BuildResult{}, errors.New(reason)
	}
	_, parsed, err := subscription.ParseURI(node.RawURI)
	if err != nil {
		return BuildResult{}, fmt.Errorf("разбор профиля: %w", err)
	}
	outbound, err := buildOutbound(parsed)
	if err != nil {
		return BuildResult{}, err
	}

	if options.HTTPPort == 0 {
		options.HTTPPort = 10809
	}
	if options.SOCKSPort == 0 {
		options.SOCKSPort = 10808
	}
	if options.APIPort == 0 {
		options.APIPort = APIPort
	}
	if options.DNS1 == "" {
		options.DNS1 = "1.1.1.1"
	}
	if options.DNS2 == "" {
		options.DNS2 = "8.8.8.8"
	}

	logSettings := map[string]any{"loglevel": "warning"}
	if options.AccessLog != "" {
		logSettings["access"] = options.AccessLog
	}
	if options.ErrorLog != "" {
		logSettings["error"] = options.ErrorLog
	}

	blockAds := options.BlockAds
	if blockAds && !hasGeosite(options.GeoDir) {
		blockAds = false
		warnings = append(warnings, "Блокировка рекламы отключена: рядом с xray.exe нет geosite.dat")
	}

	config := map[string]any{
		"log":       logSettings,
		"dns":       buildDNS(options),
		"inbounds":  buildInbounds(options),
		"outbounds": buildOutbounds(outbound),
		"routing":   buildRouting(options, blockAds),
	}

	if options.EnableStats {
		config["api"] = map[string]any{
			"tag":      "api",
			"listen":   fmt.Sprintf("127.0.0.1:%d", options.APIPort),
			"services": []string{"StatsService"},
		}
		config["stats"] = map[string]any{}
		config["policy"] = map[string]any{
			"system": map[string]any{
				"statsOutboundUplink":   true,
				"statsOutboundDownlink": true,
			},
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return BuildResult{}, err
	}
	return BuildResult{Config: data, Warnings: warnings}, nil
}

func hasGeosite(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "geosite.dat"))
	return err == nil && !info.IsDir() && info.Size() > 100_000
}

// buildDNS повторяет рабочую конфигурацию: обычные DNS-серверы и системный
// резолвер запасным. Попытка перевести это на DoH и завернуть в туннель
// ломала TUN-режим, поэтому от неё отказались.
func buildDNS(options ConfigOptions) map[string]any {
	servers := []any{}
	for _, value := range []string{options.DNS1, options.DNS2} {
		if value = strings.TrimSpace(value); value != "" {
			servers = append(servers, value)
		}
	}
	servers = append(servers, "localhost")
	return map[string]any{
		"servers":       servers,
		"queryStrategy": "UseIP",
	}
}

func buildOutbounds(proxy map[string]any) []any {
	return []any{
		proxy,
		map[string]any{
			"tag": "direct", "protocol": "freedom",
			"settings": map[string]any{"domainStrategy": "UseIP"},
		},
		map[string]any{
			"tag": "block", "protocol": "blackhole",
			"settings": map[string]any{"response": map[string]any{"type": "http"}},
		},
	}
}

func buildInbounds(options ConfigOptions) []any {
	if strings.EqualFold(options.Mode, "tun") {
		tunName := strings.TrimSpace(options.TunName)
		if tunName == "" {
			tunName = "MS7VPN"
		}
		return []any{
			map[string]any{
				"tag":      "tun-in",
				"port":     0,
				"protocol": "tun",
				"settings": map[string]any{
					"name":                   tunName,
					"desc":                   "Wintun",
					"mtu":                    1500,
					"gateway":                []string{"172.19.0.1/30"},
					"dns":                    []string{options.DNS1, options.DNS2},
					"autoSystemRoutingTable": []string{"0.0.0.0/1", "128.0.0.0/1"},
					"autoOutboundsInterface": "auto",
				},
				"sniffing": map[string]any{
					"enabled":      true,
					"destOverride": []string{"http", "tls", "quic"},
					"routeOnly":    true,
				},
			},
		}
	}
	return []any{
		map[string]any{
			"tag": "socks-in", "listen": "127.0.0.1", "port": options.SOCKSPort, "protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
			"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true},
		},
		map[string]any{
			"tag": "http-in", "listen": "127.0.0.1", "port": options.HTTPPort, "protocol": "http",
			"settings": map[string]any{"allowTransparent": false},
			"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}, "routeOnly": true},
		},
	}
}

func buildRouting(options ConfigOptions, blockAds bool) map[string]any {
	rules := []any{}
	if options.EnableStats {
		rules = append(rules, map[string]any{
			"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api",
		})
	}
	rules = append(rules,
		map[string]any{"type": "field", "ip": privateRanges, "outboundTag": "direct"},
		map[string]any{"type": "field", "domain": directDomains, "outboundTag": "direct"},
	)
	if blockAds {
		rules = append(rules, map[string]any{
			"type": "field", "domain": []string{"geosite:category-ads-all"}, "outboundTag": "block",
		})
	}
	// IPIfNonMatch — как в рабочей сборке. Менять эту стратегию без проверки
	// на живом подключении нельзя: от неё зависит и маршрутизация, и DNS.
	return map[string]any{"domainStrategy": "IPIfNonMatch", "rules": rules}
}

func buildOutbound(parsed subscription.ParsedNode) (map[string]any, error) {
	stream, err := buildStreamSettings(parsed)
	if err != nil {
		return nil, err
	}
	outbound := map[string]any{"tag": "proxy", "streamSettings": stream}

	switch parsed.Protocol {
	case "vless":
		user := map[string]any{"id": parsed.UserID, "encryption": defaultString(parsed.Encryption, "none")}
		if parsed.Flow != "" {
			user["flow"] = parsed.Flow
		}
		outbound["protocol"] = "vless"
		outbound["settings"] = map[string]any{"vnext": []any{map[string]any{
			"address": parsed.Address, "port": parsed.Port, "users": []any{user},
		}}}
	case "vmess":
		outbound["protocol"] = "vmess"
		outbound["settings"] = map[string]any{"vnext": []any{map[string]any{
			"address": parsed.Address,
			"port":    parsed.Port,
			"users": []any{map[string]any{
				"id": parsed.UserID, "alterId": parsed.AlterID,
				"security": defaultString(parsed.Encryption, "auto"),
			}},
		}}}
	case "trojan":
		outbound["protocol"] = "trojan"
		outbound["settings"] = map[string]any{"servers": []any{map[string]any{
			"address": parsed.Address, "port": parsed.Port, "password": parsed.Password,
		}}}
	case "shadowsocks":
		outbound["protocol"] = "shadowsocks"
		outbound["settings"] = map[string]any{"servers": []any{map[string]any{
			"address": parsed.Address, "port": parsed.Port,
			"method": parsed.Method, "password": parsed.Password, "uot": true,
		}}}
		delete(outbound, "streamSettings")
	case "socks":
		server := map[string]any{"address": parsed.Address, "port": parsed.Port}
		if parsed.Username != "" || parsed.Password != "" {
			server["users"] = []any{map[string]any{"user": parsed.Username, "pass": parsed.Password}}
		}
		outbound["protocol"] = "socks"
		outbound["settings"] = map[string]any{"servers": []any{server}, "version": "5"}
		delete(outbound, "streamSettings")
	default:
		return nil, fmt.Errorf("протокол %s пока не поддерживается", parsed.Protocol)
	}
	return outbound, nil
}

func buildStreamSettings(parsed subscription.ParsedNode) (map[string]any, error) {
	network := parsed.Transport
	if network == "" {
		network = "raw"
	}
	if network == "quic" {
		return nil, errors.New("старый QUIC transport удалён из актуального Xray; нужен XHTTP/H3")
	}

	stream := map[string]any{
		"network":  network,
		"security": defaultString(parsed.Security, "none"),
	}

	switch network {
	case "raw":
		if parsed.HeaderType != "" && parsed.HeaderType != "none" {
			stream["rawSettings"] = map[string]any{"header": map[string]any{"type": parsed.HeaderType}}
		}
	case "xhttp":
		xhttp := map[string]any{}
		if parsed.Host != "" {
			xhttp["host"] = parsed.Host
		}
		if parsed.Path != "" {
			xhttp["path"] = parsed.Path
		}
		if parsed.Mode != "" {
			xhttp["mode"] = parsed.Mode
		}
		if len(parsed.ExtraJSON) > 0 {
			xhttp["extra"] = parsed.ExtraJSON
		}
		stream["xhttpSettings"] = xhttp
	case "ws":
		websocket := map[string]any{}
		if parsed.Path != "" {
			websocket["path"] = parsed.Path
		}
		if parsed.Host != "" {
			websocket["headers"] = map[string]string{"Host": parsed.Host}
		}
		stream["wsSettings"] = websocket
	case "grpc":
		grpc := map[string]any{}
		if parsed.ServiceName != "" {
			grpc["serviceName"] = parsed.ServiceName
		}
		if strings.EqualFold(parsed.Mode, "multi") || strings.EqualFold(parsed.Mode, "multimode") {
			grpc["multiMode"] = true
		}
		stream["grpcSettings"] = grpc
	case "httpupgrade":
		httpUpgrade := map[string]any{}
		if parsed.Path != "" {
			httpUpgrade["path"] = parsed.Path
		}
		if parsed.Host != "" {
			httpUpgrade["host"] = parsed.Host
		}
		stream["httpupgradeSettings"] = httpUpgrade
	case "kcp":
		kcp := map[string]any{}
		if parsed.Seed != "" {
			kcp["seed"] = parsed.Seed
		}
		if parsed.HeaderType != "" {
			kcp["header"] = map[string]any{"type": parsed.HeaderType}
		}
		stream["kcpSettings"] = kcp
	default:
		return nil, fmt.Errorf("неподдерживаемый transport: %s", network)
	}

	switch strings.ToLower(parsed.Security) {
	case "", "none":
	case "tls":
		tls := map[string]any{"allowInsecure": parsed.AllowInsecure}
		if parsed.SNI != "" {
			tls["serverName"] = parsed.SNI
		}
		if parsed.Fingerprint != "" {
			tls["fingerprint"] = parsed.Fingerprint
		}
		if len(parsed.ALPN) > 0 {
			tls["alpn"] = parsed.ALPN
		}
		stream["tlsSettings"] = tls
	case "reality":
		reality := map[string]any{}
		if parsed.SNI != "" {
			reality["serverName"] = parsed.SNI
		}
		if parsed.Fingerprint != "" {
			reality["fingerprint"] = parsed.Fingerprint
		}
		if parsed.PublicKey != "" {
			reality["publicKey"] = parsed.PublicKey
		}
		if parsed.ShortID != "" {
			reality["shortId"] = parsed.ShortID
		}
		if parsed.SpiderX != "" {
			reality["spiderX"] = parsed.SpiderX
		}
		stream["realitySettings"] = reality
	default:
		return nil, fmt.Errorf("неподдерживаемая transport security: %s", parsed.Security)
	}
	return stream, nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
