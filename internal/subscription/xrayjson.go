package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Панели Remnawave и Marzban умеют отдавать подписку в формате Xray JSON:
// массив готовых конфигов, у каждого свой outbound. Happ и Shade такой формат
// понимают, поэтому MS7VPN тоже переводит каждый outbound обратно в share-ссылку
// и дальше работает с ней как с обычной ссылкой из подписки.

type jsonConfig struct {
	Remarks   string           `json:"remarks"`
	Outbounds []map[string]any `json:"outbounds"`
}

// LinksFromXrayJSON возвращает share-ссылки, собранные из Xray JSON.
func LinksFromXrayJSON(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var configs []jsonConfig
	switch text[0] {
	case '[':
		if err := json.Unmarshal([]byte(text), &configs); err != nil {
			return nil
		}
	case '{':
		var single jsonConfig
		if err := json.Unmarshal([]byte(text), &single); err != nil {
			return nil
		}
		configs = []jsonConfig{single}
	default:
		return nil
	}

	links := make([]string, 0, len(configs))
	for _, config := range configs {
		for _, outbound := range config.Outbounds {
			link := linkFromOutbound(outbound, config.Remarks)
			if link != "" {
				links = append(links, link)
				break // в одном конфиге нас интересует только исходящее соединение
			}
		}
	}
	return links
}

func linkFromOutbound(outbound map[string]any, remarks string) string {
	protocol := strings.ToLower(asString(outbound["protocol"]))
	name := remarks
	if name == "" {
		name = asString(outbound["tag"])
	}
	settings, _ := outbound["settings"].(map[string]any)
	stream, _ := outbound["streamSettings"].(map[string]any)
	if settings == nil {
		return ""
	}

	switch protocol {
	case "vless", "vmess":
		servers, _ := settings["vnext"].([]any)
		if len(servers) == 0 {
			return ""
		}
		server, _ := servers[0].(map[string]any)
		if server == nil {
			return ""
		}
		address := asString(server["address"])
		port := asInt(server["port"])
		users, _ := server["users"].([]any)
		if address == "" || port == 0 || len(users) == 0 {
			return ""
		}
		user, _ := users[0].(map[string]any)
		id := asString(user["id"])
		if id == "" {
			return ""
		}
		if protocol == "vmess" {
			return vmessLink(address, port, id, asInt(user["alterId"]), name, stream)
		}
		query := streamQuery(stream)
		if flow := asString(user["flow"]); flow != "" {
			query.Set("flow", flow)
		}
		if encryption := asString(user["encryption"]); encryption != "" {
			query.Set("encryption", encryption)
		}
		return buildLink("vless", id, address, port, query, name)

	case "trojan":
		servers, _ := settings["servers"].([]any)
		if len(servers) == 0 {
			return ""
		}
		server, _ := servers[0].(map[string]any)
		if server == nil {
			return ""
		}
		address := asString(server["address"])
		port := asInt(server["port"])
		password := asString(server["password"])
		if address == "" || port == 0 || password == "" {
			return ""
		}
		return buildLink("trojan", password, address, port, streamQuery(stream), name)

	case "shadowsocks":
		servers, _ := settings["servers"].([]any)
		if len(servers) == 0 {
			return ""
		}
		server, _ := servers[0].(map[string]any)
		if server == nil {
			return ""
		}
		address := asString(server["address"])
		port := asInt(server["port"])
		method := asString(server["method"])
		password := asString(server["password"])
		if address == "" || port == 0 || method == "" {
			return ""
		}
		userInfo := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
		link := fmt.Sprintf("ss://%s@%s:%d", userInfo, address, port)
		if name != "" {
			link += "#" + url.PathEscape(name)
		}
		return link

	case "socks":
		servers, _ := settings["servers"].([]any)
		if len(servers) == 0 {
			return ""
		}
		server, _ := servers[0].(map[string]any)
		if server == nil {
			return ""
		}
		address := asString(server["address"])
		port := asInt(server["port"])
		if address == "" || port == 0 {
			return ""
		}
		credentials := ""
		if users, _ := server["users"].([]any); len(users) > 0 {
			if user, _ := users[0].(map[string]any); user != nil {
				credentials = base64.RawURLEncoding.EncodeToString(
					[]byte(asString(user["user"]) + ":" + asString(user["pass"]))) + "@"
			}
		}
		link := fmt.Sprintf("socks://%s%s:%d", credentials, address, port)
		if name != "" {
			link += "#" + url.PathEscape(name)
		}
		return link
	}
	return ""
}

func vmessLink(address string, port int, id string, alterID int, name string, stream map[string]any) string {
	query := streamQuery(stream)
	payload := map[string]any{
		"v":    "2",
		"ps":   name,
		"add":  address,
		"port": strconv.Itoa(port),
		"id":   id,
		"aid":  strconv.Itoa(alterID),
		"net":  first(query.Get("type"), "tcp"),
		"type": first(query.Get("headerType"), "none"),
		"host": first(query.Get("host"), query.Get("sni")),
		"path": query.Get("path"),
		"tls":  query.Get("security"),
		"sni":  query.Get("sni"),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(data)
}

func buildLink(scheme, credential, address string, port int, query url.Values, name string) string {
	link := fmt.Sprintf("%s://%s@%s:%d", scheme, url.QueryEscape(credential), address, port)
	if encoded := query.Encode(); encoded != "" {
		link += "?" + encoded
	}
	if name != "" {
		link += "#" + url.PathEscape(name)
	}
	return link
}

// streamQuery переводит streamSettings обратно в параметры share-ссылки.
func streamQuery(stream map[string]any) url.Values {
	query := url.Values{}
	if stream == nil {
		return query
	}
	network := strings.ToLower(first(asString(stream["network"]), "tcp"))
	query.Set("type", network)

	security := strings.ToLower(asString(stream["security"]))
	if security == "" {
		security = "none"
	}
	query.Set("security", security)

	switch security {
	case "reality":
		if reality, _ := stream["realitySettings"].(map[string]any); reality != nil {
			setIfNotEmpty(query, "sni", asString(reality["serverName"]))
			setIfNotEmpty(query, "pbk", asString(reality["publicKey"]))
			setIfNotEmpty(query, "sid", asString(reality["shortId"]))
			setIfNotEmpty(query, "fp", asString(reality["fingerprint"]))
			setIfNotEmpty(query, "spx", asString(reality["spiderX"]))
		}
	case "tls":
		if tls, _ := stream["tlsSettings"].(map[string]any); tls != nil {
			setIfNotEmpty(query, "sni", asString(tls["serverName"]))
			setIfNotEmpty(query, "fp", asString(tls["fingerprint"]))
			if alpn, _ := tls["alpn"].([]any); len(alpn) > 0 {
				values := make([]string, 0, len(alpn))
				for _, item := range alpn {
					if s := asString(item); s != "" {
						values = append(values, s)
					}
				}
				setIfNotEmpty(query, "alpn", strings.Join(values, ","))
			}
			if allowInsecure, ok := tls["allowInsecure"].(bool); ok && allowInsecure {
				query.Set("allowInsecure", "1")
			}
		}
	}

	switch network {
	case "ws", "httpupgrade":
		key := "wsSettings"
		if network == "httpupgrade" {
			key = "httpupgradeSettings"
		}
		if settings, _ := stream[key].(map[string]any); settings != nil {
			setIfNotEmpty(query, "path", asString(settings["path"]))
			host := asString(settings["host"])
			if host == "" {
				if headers, _ := settings["headers"].(map[string]any); headers != nil {
					host = first(asString(headers["Host"]), asString(headers["host"]))
				}
			}
			setIfNotEmpty(query, "host", host)
		}
	case "grpc":
		if settings, _ := stream["grpcSettings"].(map[string]any); settings != nil {
			setIfNotEmpty(query, "serviceName", asString(settings["serviceName"]))
			if multi, ok := settings["multiMode"].(bool); ok && multi {
				query.Set("mode", "multi")
			}
		}
	case "xhttp", "splithttp":
		key := "xhttpSettings"
		if _, ok := stream["splithttpSettings"]; ok {
			key = "splithttpSettings"
		}
		if settings, _ := stream[key].(map[string]any); settings != nil {
			setIfNotEmpty(query, "path", asString(settings["path"]))
			setIfNotEmpty(query, "host", asString(settings["host"]))
			setIfNotEmpty(query, "mode", asString(settings["mode"]))
		}
	case "kcp", "mkcp":
		if settings, _ := stream["kcpSettings"].(map[string]any); settings != nil {
			setIfNotEmpty(query, "seed", asString(settings["seed"]))
			if header, _ := settings["header"].(map[string]any); header != nil {
				setIfNotEmpty(query, "headerType", asString(header["type"]))
			}
		}
	case "tcp", "raw":
		key := "tcpSettings"
		if _, ok := stream["rawSettings"]; ok {
			key = "rawSettings"
		}
		if settings, _ := stream[key].(map[string]any); settings != nil {
			if header, _ := settings["header"].(map[string]any); header != nil {
				headerType := asString(header["type"])
				setIfNotEmpty(query, "headerType", headerType)
				if headerType == "http" {
					if request, _ := header["request"].(map[string]any); request != nil {
						if paths, _ := request["path"].([]any); len(paths) > 0 {
							setIfNotEmpty(query, "path", asString(paths[0]))
						}
						if headers, _ := request["headers"].(map[string]any); headers != nil {
							if hosts, _ := headers["Host"].([]any); len(hosts) > 0 {
								setIfNotEmpty(query, "host", asString(hosts[0]))
							}
						}
					}
				}
			}
		}
	}
	return query
}

func setIfNotEmpty(query url.Values, key, value string) {
	if strings.TrimSpace(value) != "" {
		query.Set(key, value)
	}
}
