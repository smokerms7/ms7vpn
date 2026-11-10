package xray

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ms7vpn/internal/model"
	"ms7vpn/internal/splitroute"
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
	Mode          string
	HTTPPort      int
	SOCKSPort     int
	APIPort       int
	DNS1          string
	DNS2          string
	BlockAds      bool
	AccessLog     string
	ErrorLog      string
	TunName       string
	BindInterface string
	ServerIP      string

	// GeoDir — папка, где лежат geosite.dat/geoip.dat. Нужна только для
	// блокировки рекламы; если файлов нет, правило не добавляется, иначе
	// Xray откажется стартовать целиком.
	GeoDir string

	// EnableStats включает локальный API со счётчиками трафика.
	EnableStats bool

	// «Без VPN»: российские сайты и списки пользователя. Домены уже
	// очищены splitroute.CleanDomains.
	DirectRu      bool
	DirectDomains []string
	ProxyDomains  []string

	// NativeTun — TUN самого Xray, как в Shade: вход tun на wintun, DNS из
	// туннеля — в dns-out. Маршруты в туннель и DNS адаптера ставит tunnet.
	// SendThrough — адрес физического адаптера: прямой трафик выходит через
	// него, а не обратно в туннель. DirectApps — программы мимо VPN.
	NativeTun   bool
	SendThrough string
	DirectApps  []string
}

// TunAdapterName — имя адаптера встроенного TUN Xray.
const TunAdapterName = "MS7VPN-XRAY"

// TunAdapterNames — основное имя и запасные. Xray выводит идентификатор
// адаптера из имени (MD5), и после аварийного завершения Windows иногда
// держит это имя занятым до перезагрузки («Cannot create a file when that
// file already exists»). Тогда подключаемся под запасным именем.
var TunAdapterNames = []string{TunAdapterName, "MS7VPN-XRAY-2", "MS7VPN-XRAY-3", "MS7VPN-XRAY-4"}

func tunName(name string) string {
	if name == "" {
		return TunAdapterName
	}
	return name
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
	if parsed.Protocol == "hysteria2" {
		return BuildResult{}, errors.New("Hysteria2 требует ядро sing-box — выберите его в настройках")
	}
	outbound, err := buildOutbound(parsed)
	if err != nil {
		return BuildResult{}, err
	}
	// The protocol engine must use the physical interface while sing-box
	// owns TUN routes. Resolve the server before TUN to avoid DNS recursion.
	if options.ServerIP != "" {
		settings := outbound["settings"].(map[string]any)
		for _, key := range []string{"vnext", "servers"} {
			if servers, ok := settings[key].([]any); ok && len(servers) > 0 {
				servers[0].(map[string]any)["address"] = options.ServerIP
			}
		}
	}
	if options.BindInterface != "" {
		stream, _ := outbound["streamSettings"].(map[string]any)
		if stream == nil {
			stream = map[string]any{}
			outbound["streamSettings"] = stream
		}
		stream["sockopt"] = map[string]any{"interface": options.BindInterface}
	}
	if strings.EqualFold(options.Mode, "tun") && !options.NativeTun {
		return BuildResult{}, errors.New("TUN управляется сетевым слоем sing-box; запустите Xray с локальным SOCKS")
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

	useGeoIP := options.DirectRu && hasGeoIP(options.GeoDir)
	geo := geoAvailability{site: hasGeosite(options.GeoDir), ip: hasGeoIP(options.GeoDir)}
	if userRulesNeedGeo(options.DirectDomains, options.ProxyDomains, geo) {
		warnings = append(warnings, "Правила geosite/geoip пропущены: рядом с xray.exe нет geosite.dat или geoip.dat (они есть в полной установке)")
	}
	outbounds := applyTransportExtras(buildOutbounds(outbound))
	if options.NativeTun {
		direct := outbounds[1].(map[string]any)
		if options.SendThrough != "" {
			direct["sendThrough"] = options.SendThrough
		}
		outbounds = append(outbounds, map[string]any{"tag": "dns-out", "protocol": "dns"})
	} else if options.BindInterface != "" {
		// В TUN прямой выход тоже должен идти мимо адаптера, иначе трафик
		// «Без VPN» вернулся бы в туннель и зациклился.
		direct := outbounds[1].(map[string]any)
		direct["streamSettings"] = map[string]any{"sockopt": map[string]any{"interface": options.BindInterface}}
	}
	inbounds := buildInbounds(options)
	if options.NativeTun {
		tun := map[string]any{
			"tag": "tun-in", "protocol": "tun",
			// Адрес адаптеру ставит само ядро сразу после создания: без него
			// Windows выдаёт 169.254.x.x и трафик идёт не сразу.
			"settings": map[string]any{"name": tunName(options.TunName), "mtu": TunMTU, "gateway": []string{"198.18.0.1/30"}},
			"sniffing": sniffing("http", "tls", "quic"),
		}
		inbounds = append([]any{tun}, inbounds...)
	}
	config := map[string]any{
		"log":       logSettings,
		"dns":       buildDNS(options),
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   buildRouting(options, blockAds, useGeoIP, geo),
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

func hasGeoIP(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "geoip.dat"))
	return err == nil && !info.IsDir() && info.Size() > 100_000
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

	return []any{
		map[string]any{
			"tag": "socks-in", "listen": InboundListen, "port": options.SOCKSPort, "protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
			"sniffing": sniffing("http", "tls", "quic"),
		},
		map[string]any{
			"tag": "http-in", "listen": InboundListen, "port": options.HTTPPort, "protocol": "http",
			"settings": map[string]any{"allowTransparent": false},
			"sniffing": sniffing("http", "tls"),
		},
	}
}

type geoAvailability struct{ site, ip bool }

func userRulesNeedGeo(direct, proxy []string, geo geoAvailability) bool {
	for _, list := range [][]string{direct, proxy} {
		_, _, _, sites, ips := splitroute.Split(list)
		if (len(sites) > 0 && !geo.site) || (len(ips) > 0 && !geo.ip) {
			return true
		}
	}
	return false
}

// userRules — правила Xray из списка пользователя: доменные и адресные
// отдельно, потому что в одном правиле Xray требует совпадения обоих.
func userRules(list []string, outbound string, geo geoAvailability) []any {
	domains, keywords, cidrs, sites, ips := splitroute.Split(list)
	names := domainRules(domains)
	for _, keyword := range keywords {
		names = append(names, "keyword:"+keyword)
	}
	if geo.site {
		for _, site := range sites {
			names = append(names, "geosite:"+site)
		}
	}
	addresses := append([]string{}, cidrs...)
	if geo.ip {
		for _, code := range ips {
			addresses = append(addresses, "geoip:"+code)
		}
	}
	var rules []any
	if len(names) > 0 {
		rules = append(rules, map[string]any{"type": "field", "domain": names, "outboundTag": outbound})
	}
	if len(addresses) > 0 {
		rules = append(rules, map[string]any{"type": "field", "ip": addresses, "outboundTag": outbound})
	}
	return rules
}

func buildRouting(options ConfigOptions, blockAds, useGeoIP bool, geo geoAvailability) map[string]any {
	rules := []any{}
	if options.EnableStats {
		rules = append(rules, map[string]any{
			"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api",
		})
	}
	if options.NativeTun {
		// DNS-запросы системы из туннеля отвечает DNS-модуль Xray.
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"tun-in"}, "network": "udp", "port": 53, "outboundTag": "dns-out"})
		if names := processNames(options.DirectApps); len(names) > 0 {
			rules = append(rules, map[string]any{"type": "field", "process": names, "outboundTag": "direct"})
		}
	}
	rules = append(rules,
		map[string]any{"type": "field", "ip": privateRanges, "outboundTag": "direct"},
		map[string]any{"type": "field", "domain": directDomains, "outboundTag": "direct"},
	)
	// «Через VPN» пользователя — раньше всего остального: этим списком
	// человек вытаскивает отдельный сайт из-под «российских напрямую».
	rules = append(rules, userRules(options.ProxyDomains, "proxy", geo)...)
	if blockAds {
		rules = append(rules, map[string]any{
			"type": "field", "domain": []string{"geosite:category-ads-all"}, "outboundTag": "block",
		})
	}
	rules = append(rules, userRules(options.DirectDomains, "direct", geo)...)
	if options.DirectRu {
		rules = append(rules, map[string]any{
			"type": "field", "domain": domainRules(append(append([]string{}, splitroute.RuZones...), splitroute.RuServices...)), "outboundTag": "direct",
		})
		if useGeoIP {
			rules = append(rules, map[string]any{
				"type": "field", "ip": []string{"geoip:ru"}, "outboundTag": "direct",
			})
		}
	}
	return map[string]any{"domainStrategy": routingStrategyFor(rules), "rules": rules}
}

// routingStrategyFor выбирает стратегию, если пользователь её не задал.
// IPIfNonMatch заставляет Xray перед каждым новым доменом спрашивать DNS
// (а DNS идёт через сервер) — лишний круг до сервера на каждый сайт, отсюда
// «тугое» соединение. Резолв нужен, только когда есть IP-правила кроме
// локальных сетей (geoip:ru, IP/geoip из списков пользователя); иначе AsIs:
// трафик из TUN и так приходит с IP, а домены локалки ловит правило domain.
func routingStrategyFor(rules []any) string {
	if DomainStrategy != "" {
		return routingStrategy()
	}
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		ips, ok := rule["ip"].([]string)
		if !ok || len(ips) == 0 {
			continue
		}
		if len(ips) == len(privateRanges) && ips[0] == privateRanges[0] {
			continue
		}
		return "IPIfNonMatch"
	}
	return "AsIs"
}

// processNames — имена программ для правила process. Xray сравнивает с
// учётом регистра, а Windows — нет, поэтому каждое имя даётся в нескольких
// написаниях: как есть, строчными и с заглавной.
func processNames(apps []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, app := range apps {
		lower := strings.ToLower(app)
		add(app)
		add(lower)
		if lower != "" {
			add(strings.ToUpper(lower[:1]) + lower[1:])
		}
	}
	return out
}

// domainRules превращает домены в правила Xray: «domain:» совпадает с самим
// доменом и всеми поддоменами.
func domainRules(domains []string) []string {
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		out = append(out, "domain:"+domain)
	}
	return out
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
