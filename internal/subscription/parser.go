package subscription

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"ms7vpn/internal/model"
)

var (
	shareSchemeRE   = regexp.MustCompile(`(?i)^(vless|vmess|trojan|ss|socks|hysteria2|hy2)://`)
	embeddedLinksRE = regexp.MustCompile(`(?i)(vless|vmess|trojan|ss|socks|hysteria2|hy2)://[^\s<>"']+`)
)

// ParsedNode is the normalized representation used by the Xray config builder.
// RawURI remains the source of truth, so a subscription refresh never loses
// protocol-specific parameters that the UI does not display.
type ParsedNode struct {
	Protocol             string
	Name                 string
	Address              string
	Port                 int
	UserID               string
	Username             string
	Password             string
	Encryption           string
	Flow                 string
	Method               string
	AlterID              int
	Transport            string
	Security             string
	SNI                  string
	Fingerprint          string
	PublicKey            string
	ShortID              string
	SpiderX              string
	ALPN                 []string
	AllowInsecure        bool
	Host                 string
	Path                 string
	Mode                 string
	ServiceName          string
	HeaderType           string
	Seed                 string
	ExtraJSON            map[string]any
	HysteriaAuth         string
	HysteriaObfs         string
	HysteriaObfsPassword string
	Meta                 map[string]string
}

// ParseContent accepts the common subscription formats returned by Remnawave:
// plain share-link lists, base64 encoded lists, and JSON arrays of links.
func ParseContent(raw []byte) ([]model.Node, []string, error) {
	text := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
	if text == "" {
		return nil, nil, errors.New("пустой ответ подписки")
	}

	// JSON array: ["vless://...", {"url":"trojan://..."}]
	if strings.HasPrefix(text, "[") {
		var arr []any
		if json.Unmarshal([]byte(text), &arr) == nil {
			var lines []string
			for _, item := range arr {
				switch v := item.(type) {
				case string:
					lines = append(lines, v)
				case map[string]any:
					for _, key := range []string{"url", "link", "uri"} {
						if s, ok := v[key].(string); ok && s != "" {
							lines = append(lines, s)
							break
						}
					}
				}
			}
			if len(lines) > 0 {
				text = strings.Join(lines, "\n")
			}
		}
	}

	// Панель может отдавать подписку в формате Xray JSON (массив готовых конфигов).
	// Переводим каждый outbound обратно в share-ссылку — так же поступают Happ и Shade.
	if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") {
		if links := LinksFromXrayJSON(text); len(links) > 0 {
			text = strings.Join(links, "\n")
		}
	}

	// Полный Xray JSON с маршрутизацией переносить по частям нельзя:
	// это меняет правила DNS и routing.
	if strings.HasPrefix(text, "{") {
		var obj map[string]any
		if json.Unmarshal([]byte(text), &obj) == nil {
			if _, ok := obj["outbounds"]; ok {
				return nil, []string{"Получен полный Xray JSON с маршрутизацией. Импорт такого файла 1:1 пока не поддерживается."}, errors.New("полный Xray JSON пока не поддерживается")
			}
			if links, ok := obj["links"].([]any); ok {
				var lines []string
				for _, item := range links {
					if s, ok := item.(string); ok {
						lines = append(lines, s)
					}
				}
				if len(lines) > 0 {
					text = strings.Join(lines, "\n")
				}
			}
		}
	}

	if !strings.Contains(text, "://") {
		if decoded, ok := decodeBase64Loose(text); ok && strings.Contains(decoded, "://") {
			text = decoded
		}
	}

	candidates := splitCandidateLinks(text)
	if len(candidates) == 0 {
		return nil, nil, errors.New("в подписке не найдено ссылок VLESS/VMess/Trojan/Shadowsocks/SOCKS/Hysteria2")
	}

	seen := make(map[string]struct{}, len(candidates))
	nodes := make([]model.Node, 0, len(candidates))
	warnings := make([]string, 0)
	for _, rawURI := range candidates {
		rawURI = strings.TrimSpace(rawURI)
		if rawURI == "" || !shareSchemeRE.MatchString(rawURI) {
			continue
		}
		if _, ok := seen[rawURI]; ok {
			continue
		}
		seen[rawURI] = struct{}{}
		node, _, err := ParseURI(rawURI)
		if err != nil {
			warnings = append(warnings, shortWarning(rawURI, err))
			continue
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, warnings, errors.New("ни одна ссылка в подписке не была распознана")
	}
	return nodes, warnings, nil
}

func splitCandidateLinks(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var out []string
	for _, part := range strings.Split(text, "\n") {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "#") {
			continue
		}
		if shareSchemeRE.MatchString(part) {
			out = append(out, part)
			continue
		}
		out = append(out, embeddedLinksRE.FindAllString(part, -1)...)
	}
	return out
}

func ParseURI(raw string) (model.Node, ParsedNode, error) {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	var p ParsedNode
	var err error
	switch {
	case strings.HasPrefix(lower, "vless://"):
		p, err = parseVLESSOrTrojan(raw, "vless")
	case strings.HasPrefix(lower, "trojan://"):
		p, err = parseVLESSOrTrojan(raw, "trojan")
	case strings.HasPrefix(lower, "vmess://"):
		p, err = parseVMess(raw)
	case strings.HasPrefix(lower, "ss://"):
		p, err = parseShadowsocks(raw)
	case strings.HasPrefix(lower, "socks://"):
		p, err = parseSOCKS(raw)
	case strings.HasPrefix(lower, "hysteria2://"), strings.HasPrefix(lower, "hy2://"):
		p, err = parseHysteria2(raw)
	default:
		err = errors.New("неизвестная схема")
	}
	if err != nil {
		return model.Node{}, ParsedNode{}, err
	}

	supported, reason := validateSupport(p)
	node := model.Node{
		ID:                StableID(raw),
		Name:              fallbackName(p),
		Protocol:          p.Protocol,
		Address:           p.Address,
		Port:              p.Port,
		Transport:         p.Transport,
		Security:          p.Security,
		SNI:               p.SNI,
		RawURI:            raw,
		Supported:         supported,
		UnsupportedReason: reason,
		Meta:              p.Meta,
	}
	return node, p, nil
}

func validateSupport(p ParsedNode) (bool, string) {
	if p.Address == "" || p.Port <= 0 || p.Port > 65535 {
		return false, "не указан корректный адрес или порт"
	}
	switch p.Protocol {
	case "vless":
		if p.UserID == "" {
			return false, "в VLESS отсутствует UUID"
		}
	case "vmess":
		if p.UserID == "" {
			return false, "в VMess отсутствует UUID"
		}
	case "trojan":
		if p.Password == "" {
			return false, "в Trojan отсутствует пароль"
		}
	case "shadowsocks":
		if p.Method == "" || p.Password == "" {
			return false, "в Shadowsocks отсутствует метод или пароль"
		}
		if p.Meta["plugin"] != "" {
			return false, "SIP003 plugin пока не поддерживается"
		}
	case "socks":
	case "hysteria2":
		// Hysteria2 — это протокол sing-box/mihomo. В Xray-core его нет, и
		// раньше приложение молча собирало конфиг, который ядро отклоняло.
		// Честнее сразу сказать, что такой сервер здесь не заработает.
		return false, "Hysteria2 не поддерживается ядром Xray — выберите VLESS, VMess, Trojan или Shadowsocks"
	default:
		return false, "протокол пока не поддерживается"
	}

	switch p.Transport {
	case "", "raw", "xhttp", "ws", "grpc", "httpupgrade", "kcp":
	default:
		return false, "неподдерживаемый transport: " + p.Transport + " (QUIC/legacy transport не поддерживается)"
	}
	if p.Security == "reality" && p.Transport != "raw" && p.Transport != "xhttp" && p.Transport != "grpc" {
		return false, "REALITY работает только с RAW/TCP, XHTTP и gRPC"
	}
	return true, ""
}

func parseVLESSOrTrojan(raw, protocol string) (ParsedNode, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ParsedNode{}, err
	}
	q := u.Query()
	p := ParsedNode{Protocol: protocol, Meta: map[string]string{}}
	p.Address = u.Hostname()
	p.Port = parsePort(u.Port(), defaultPort(q.Get("security")))
	if u.User != nil {
		if protocol == "vless" {
			p.UserID = u.User.Username()
		} else {
			p.Password = u.User.Username()
			if pass, ok := u.User.Password(); ok && pass != "" {
				p.Password += ":" + pass
			}
		}
	}
	p.Name = decodeFragment(u.Fragment)
	copyMeta(p.Meta, q)
	p.Transport = normalizeTransport(first(q.Get("type"), q.Get("network")))
	p.Security = strings.ToLower(first(q.Get("security"), q.Get("tls")))
	if p.Security == "1" {
		p.Security = "tls"
	}
	if p.Security == "" {
		p.Security = "none"
	}
	p.SNI = first(q.Get("sni"), q.Get("serverName"), q.Get("peer"))
	p.Fingerprint = first(q.Get("fp"), q.Get("fingerprint"))
	p.PublicKey = first(q.Get("pbk"), q.Get("publicKey"))
	p.ShortID = first(q.Get("sid"), q.Get("shortId"))
	p.SpiderX = first(q.Get("spx"), q.Get("spiderX"))
	p.Flow = q.Get("flow")
	p.Encryption = first(q.Get("encryption"), "none")
	p.ALPN = splitCSV(q.Get("alpn"))
	p.AllowInsecure = parseBool(first(q.Get("allowInsecure"), q.Get("insecure")))
	p.Host = q.Get("host")
	p.Path = q.Get("path")
	p.Mode = q.Get("mode")
	p.ServiceName = first(q.Get("serviceName"), q.Get("service_name"))
	p.HeaderType = first(q.Get("headerType"), q.Get("header"))
	p.Seed = q.Get("seed")
	if extra := q.Get("extra"); extra != "" {
		var object map[string]any
		if json.Unmarshal([]byte(extra), &object) == nil {
			p.ExtraJSON = object
		}
	}
	if p.Address == "" {
		return ParsedNode{}, errors.New("отсутствует адрес")
	}
	return p, nil
}

func parseVMess(raw string) (ParsedNode, error) {
	payload := strings.TrimSpace(strings.TrimPrefix(raw, "vmess://"))
	decoded, ok := decodeBase64Loose(payload)
	if !ok {
		return ParsedNode{}, errors.New("неверный base64 VMess")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(decoded), &v); err != nil {
		return ParsedNode{}, fmt.Errorf("VMess JSON: %w", err)
	}
	p := ParsedNode{Protocol: "vmess", Meta: map[string]string{}}
	p.Name = asString(v["ps"])
	p.Address = asString(v["add"])
	p.Port = asInt(v["port"])
	p.UserID = asString(v["id"])
	p.AlterID = asInt(v["aid"])
	p.Encryption = first(asString(v["scy"]), "auto")
	p.Transport = normalizeTransport(asString(v["net"]))
	p.HeaderType = asString(v["type"])
	p.Host = asString(v["host"])
	p.Path = asString(v["path"])
	p.Security = strings.ToLower(asString(v["tls"]))
	if p.Security == "" {
		p.Security = "none"
	}
	p.SNI = first(asString(v["sni"]), asString(v["peer"]))
	p.Fingerprint = asString(v["fp"])
	p.ALPN = splitCSV(asString(v["alpn"]))
	for k, value := range v {
		p.Meta[k] = asString(value)
	}
	return p, nil
}

func parseShadowsocks(raw string) (ParsedNode, error) {
	p := ParsedNode{Protocol: "shadowsocks", Transport: "raw", Security: "none", Meta: map[string]string{}}
	rest := strings.TrimPrefix(raw, "ss://")
	if i := strings.Index(rest, "#"); i >= 0 {
		p.Name = decodeFragment(rest[i+1:])
		rest = rest[:i]
	}
	var query string
	if i := strings.Index(rest, "?"); i >= 0 {
		query = rest[i+1:]
		rest = rest[:i]
	}
	q, _ := url.ParseQuery(query)
	copyMeta(p.Meta, q)

	var credentials, endpoint string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		credentials, endpoint = rest[:at], rest[at+1:]
		if decoded, ok := decodeBase64Loose(credentials); ok {
			credentials = decoded
		}
	} else {
		decoded, ok := decodeBase64Loose(rest)
		if !ok {
			return ParsedNode{}, errors.New("неверный Shadowsocks base64")
		}
		at := strings.LastIndex(decoded, "@")
		if at < 0 {
			return ParsedNode{}, errors.New("в Shadowsocks отсутствует endpoint")
		}
		credentials, endpoint = decoded[:at], decoded[at+1:]
	}
	credentials, _ = url.QueryUnescape(credentials)
	colon := strings.Index(credentials, ":")
	if colon < 1 {
		return ParsedNode{}, errors.New("в Shadowsocks отсутствует method:password")
	}
	p.Method = credentials[:colon]
	p.Password = credentials[colon+1:]
	p.Address, p.Port = splitEndpoint(endpoint)
	p.Meta["plugin"] = q.Get("plugin")
	return p, nil
}

func parseSOCKS(raw string) (ParsedNode, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ParsedNode{}, err
	}
	p := ParsedNode{Protocol: "socks", Transport: "raw", Security: "none", Meta: map[string]string{}}
	p.Address = u.Hostname()
	p.Port = parsePort(u.Port(), 1080)
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	p.Name = decodeFragment(u.Fragment)
	copyMeta(p.Meta, u.Query())
	return p, nil
}

func parseHysteria2(raw string) (ParsedNode, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ParsedNode{}, err
	}
	q := u.Query()
	p := ParsedNode{Protocol: "hysteria2", Transport: "hysteria", Security: "tls", Meta: map[string]string{}}
	p.Address = u.Hostname()
	p.Port = parsePort(u.Port(), 443)
	if u.User != nil {
		p.HysteriaAuth = u.User.Username()
		if pass, ok := u.User.Password(); ok && pass != "" {
			p.HysteriaAuth += ":" + pass
		}
	}
	p.Name = decodeFragment(u.Fragment)
	copyMeta(p.Meta, q)
	p.SNI = first(q.Get("sni"), q.Get("peer"))
	p.AllowInsecure = parseBool(first(q.Get("insecure"), q.Get("allowInsecure")))
	p.ALPN = splitCSV(q.Get("alpn"))
	p.HysteriaObfs = strings.ToLower(q.Get("obfs"))
	p.HysteriaObfsPassword = first(q.Get("obfs-password"), q.Get("obfsPassword"))
	return p, nil
}

func normalizeTransport(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "tcp", "raw":
		return "raw"
	case "xhttp", "splithttp", "split-http":
		return "xhttp"
	case "ws", "websocket":
		return "ws"
	case "grpc":
		return "grpc"
	case "httpupgrade", "http-upgrade", "http_up":
		return "httpupgrade"
	case "kcp", "mkcp":
		return "kcp"
	case "hysteria", "hysteria2", "hy2":
		return "hysteria"
	case "quic":
		return "quic"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func StableID(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:12])
}

func fallbackName(p ParsedNode) string {
	if strings.TrimSpace(p.Name) != "" {
		return strings.TrimSpace(p.Name)
	}
	if p.Address != "" {
		return strings.ToUpper(p.Protocol) + " · " + p.Address
	}
	return strings.ToUpper(p.Protocol)
}

func decodeBase64Loose(value string) (string, bool) {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	candidates := []string{value}
	if mod := len(value) % 4; mod != 0 {
		candidates = append(candidates, value+strings.Repeat("=", 4-mod))
	}
	encodings := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}
	for _, candidate := range candidates {
		for _, encoding := range encodings {
			decoded, err := encoding.DecodeString(candidate)
			if err == nil && len(decoded) > 0 {
				return string(decoded), true
			}
		}
	}
	return "", false
}

func ParseSubscriptionUserInfo(header string) model.UserInfo {
	var info model.UserInfo
	for _, part := range strings.Split(header, ";") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			continue
		}
		value, _ := strconv.ParseInt(strings.TrimSpace(pair[1]), 10, 64)
		switch strings.ToLower(strings.TrimSpace(pair[0])) {
		case "upload":
			info.UploadBytes = value
		case "download":
			info.DownloadBytes = value
		case "total":
			info.TotalBytes = value
		case "expire":
			info.ExpireUnix = value
		}
	}
	return info
}

func ParseProfileTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	// Remnawave отдаёт заголовок как base64:<строка> — здесь декодируем всегда.
	if rest, ok := strings.CutPrefix(value, "base64:"); ok {
		if decoded, ok := decodeBase64Loose(strings.TrimSpace(rest)); ok && looksLikeTitle(decoded) {
			return strings.TrimSpace(decoded)
		}
		value = strings.TrimSpace(rest)
	}
	// Без явного префикса декодируем только то, что действительно похоже на
	// base64. Иначе обычное имя вроде «MS7VPN» проходит как валидный base64
	// и превращается в мусор.
	if looksLikeBase64(value) {
		if decoded, ok := decodeBase64Loose(value); ok && looksLikeTitle(decoded) {
			return strings.TrimSpace(decoded)
		}
	}
	if decoded, err := url.QueryUnescape(value); err == nil {
		return decoded
	}
	return value
}

// looksLikeBase64 — строка состоит только из символов base64-алфавита и
// достаточно длинная, чтобы это не было случайным совпадением.
func looksLikeBase64(value string) bool {
	if len(value) < 8 {
		return false
	}
	body := strings.TrimRight(value, "=")
	if body == "" {
		return false
	}
	for _, r := range body {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+', r == '/', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// looksLikeTitle — результат декодирования годится как название подписки:
// корректный UTF-8, печатный и содержит хотя бы одну букву.
func looksLikeTitle(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	if !isMostlyPrintable(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func isMostlyPrintable(value string) bool {
	runes := []rune(value)
	if len(runes) == 0 {
		return false
	}
	good := 0
	for _, r := range runes {
		if unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t' {
			good++
		}
	}
	return float64(good)/float64(len(runes)) > 0.9
}

func splitEndpoint(endpoint string) (string, int) {
	endpoint, _ = url.QueryUnescape(endpoint)
	if host, port, err := net.SplitHostPort(endpoint); err == nil {
		return strings.Trim(host, "[]"), parsePort(port, 0)
	}
	if index := strings.LastIndex(endpoint, ":"); index > 0 {
		return strings.Trim(endpoint[:index], "[]"), parsePort(endpoint[index+1:], 0)
	}
	return strings.Trim(endpoint, "[]"), 0
}

func decodeFragment(value string) string {
	value = strings.TrimPrefix(value, "#")
	decoded, err := url.QueryUnescape(value)
	if err == nil {
		return decoded
	}
	return value
}

func defaultPort(security string) int {
	if strings.EqualFold(security, "tls") || strings.EqualFold(security, "reality") {
		return 443
	}
	return 80
}

func parsePort(value string, fallback int) int {
	port, err := strconv.Atoi(value)
	if err == nil && port > 0 && port <= 65535 {
		return port
	}
	return fallback
}

func splitCSV(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func parseBool(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func copyMeta(destination map[string]string, values url.Values) {
	for key, entries := range values {
		if len(entries) > 0 {
			destination[key] = entries[len(entries)-1]
		}
	}
}

func asString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func asInt(value any) int {
	result, _ := strconv.Atoi(asString(value))
	return result
}

func shortWarning(raw string, err error) string {
	name := raw
	if len(name) > 56 {
		name = name[:56] + "…"
	}
	return name + ": " + err.Error()
}
