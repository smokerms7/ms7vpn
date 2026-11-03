package singbox

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"ms7vpn/internal/model"
	"ms7vpn/internal/subscription"
)

const TunName = "MS7VPN-TUN"
const TunIPv4 = "172.28.217.1"

type Options struct {
	Mode                         string
	HTTPPort, SOCKSPort, APIPort int
	Secret                       string
	DNS1, DNS2                   string
	Interface                    string
	// When set, Xray is the selected protocol engine; sing-box only supplies TUN.
	XraySOCKSPort int
	BlockAds      bool
}

func Build(node model.Node, o Options) ([]byte, error) {
	if o.DNS1 == "" {
		o.DNS1 = "1.1.1.1"
	}
	if o.DNS2 == "" {
		o.DNS2 = "8.8.8.8"
	}
	for _, dns := range []string{o.DNS1, o.DNS2} {
		if net.ParseIP(dns) == nil {
			return nil, fmt.Errorf("DNS должен быть IP-адресом: %s", dns)
		}
	}
	var outbound map[string]any
	var err error
	if o.XraySOCKSPort > 0 {
		outbound = map[string]any{"type": "socks", "tag": "proxy", "server": "127.0.0.1", "server_port": o.XraySOCKSPort, "version": "5"}
	} else {
		outbound, err = Outbound(node)
		if err != nil {
			return nil, err
		}
	}
	inbounds := []any{}
	if o.Mode == "tun" {
		inbounds = append(inbounds, map[string]any{
			"type": "tun", "tag": "tun-in", "interface_name": TunName,
			"address": []string{TunIPv4 + "/30", "fd7a:7:217::1/126"}, "mtu": 1500,
			"auto_route": true, "strict_route": true, "dns_mode": "hijack", "stack": "mixed",
			"route_address":         []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"},
			"route_exclude_address": []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "224.0.0.0/4", "255.255.255.255/32", "fc00::/7", "fe80::/10", "ff00::/8"},
		})
	} else {
		inbounds = append(inbounds, map[string]any{"type": "http", "tag": "http-in", "listen": "127.0.0.1", "listen_port": o.HTTPPort}, map[string]any{"type": "socks", "tag": "socks-in", "listen": "127.0.0.1", "listen_port": o.SOCKSPort})
	}
	rules := []any{map[string]any{"action": "sniff"}, map[string]any{"protocol": "dns", "action": "hijack-dns"}, map[string]any{"ip_is_private": true, "action": "route", "outbound": "direct"}}
	if o.BlockAds {
		// An embedded baseline works offline; do not silently download third-party lists.
		rules = append(rules, map[string]any{"domain_suffix": []string{"doubleclick.net", "googlesyndication.com", "googleadservices.com", "adservice.google.com", "app-measurement.com"}, "action": "reject"})
	}
	route := map[string]any{"rules": rules, "final": "proxy", "default_domain_resolver": map[string]any{"server": "bootstrap", "strategy": "prefer_ipv4"}}
	if o.Interface != "" {
		route["default_interface"] = o.Interface
	} else if o.Mode == "tun" {
		route["auto_detect_interface"] = true
	}
	bootstrap := map[string]any{"type": "udp", "tag": "bootstrap", "server": o.DNS2}
	if o.Interface != "" {
		bootstrap["bind_interface"] = o.Interface
	}
	config := map[string]any{
		"log":       map[string]any{"level": "info", "timestamp": true},
		"inbounds":  inbounds,
		"outbounds": []any{outbound, map[string]any{"type": "direct", "tag": "direct"}},
		"route":     route,
		"dns": map[string]any{"servers": []any{
			bootstrap,
			map[string]any{"type": "udp", "tag": "remote", "server": o.DNS1, "detour": "proxy"},
		}, "final": "remote", "strategy": "prefer_ipv4"},
		"experimental": map[string]any{"clash_api": map[string]any{"external_controller": fmt.Sprintf("127.0.0.1:%d", o.APIPort), "secret": o.Secret, "access_control_allow_origin": []string{"http://127.0.0.1"}}},
	}
	return json.MarshalIndent(config, "", "  ")
}

func Outbound(node model.Node) (map[string]any, error) {
	_, p, err := subscription.ParseURI(node.RawURI)
	if err != nil {
		return nil, err
	}
	if !node.Supported && p.Protocol != "hysteria2" {
		return nil, fmt.Errorf("%s", node.UnsupportedReason)
	}
	out := map[string]any{"type": p.Protocol, "tag": "proxy", "server": p.Address, "server_port": p.Port}
	switch p.Protocol {
	case "vless":
		if p.Encryption != "" && p.Encryption != "none" {
			return nil, fmt.Errorf("шифрование VLESS %s требует Xray", p.Encryption)
		}
		out["uuid"] = p.UserID
		if p.Flow != "" {
			out["flow"] = p.Flow
		}
	case "vmess":
		out["uuid"] = p.UserID
		out["security"] = p.Encryption
		if p.Encryption == "" {
			out["security"] = "auto"
		}
		out["alter_id"] = p.AlterID
	case "trojan":
		out["password"] = p.Password
	case "shadowsocks":
		out["method"] = p.Method
		out["password"] = p.Password
	case "socks":
		out["username"] = p.Username
		out["password"] = p.Password
		out["version"] = "5"
	case "hysteria2":
		if p.HysteriaAuth == "" {
			return nil, fmt.Errorf("в Hysteria2 отсутствует пароль")
		}
		out["password"] = p.HysteriaAuth
		if p.HysteriaObfs != "" {
			out["obfs"] = map[string]any{"type": p.HysteriaObfs, "password": p.HysteriaObfsPassword}
		}
	default:
		return nil, fmt.Errorf("протокол %s не поддерживается sing-box", p.Protocol)
	}
	if p.Protocol == "socks" || p.Protocol == "shadowsocks" {
		return out, nil
	}
	switch p.Transport {
	case "", "raw", "hysteria":
		if p.HeaderType != "" && p.HeaderType != "none" {
			return nil, fmt.Errorf("TCP header %s требует Xray", p.HeaderType)
		}
	case "ws":
		tr := map[string]any{"type": "ws", "path": p.Path}
		if p.Host != "" {
			tr["headers"] = map[string]string{"Host": p.Host}
		}
		out["transport"] = tr
	case "grpc":
		if strings.Contains(strings.ToLower(p.Mode), "multi") {
			return nil, fmt.Errorf("gRPC multiMode требует Xray")
		}
		out["transport"] = map[string]any{"type": "grpc", "service_name": p.ServiceName}
	case "httpupgrade":
		out["transport"] = map[string]any{"type": "httpupgrade", "host": p.Host, "path": p.Path}
	default:
		return nil, fmt.Errorf("транспорт %s не поддерживается sing-box — выберите Xray", p.Transport)
	}
	if p.Security != "" && p.Security != "none" {
		if p.Security != "tls" && p.Security != "reality" {
			return nil, fmt.Errorf("неподдерживаемая защита: %s", p.Security)
		}
		tls := map[string]any{"enabled": true, "server_name": p.SNI, "insecure": p.AllowInsecure}
		if len(p.ALPN) > 0 {
			tls["alpn"] = p.ALPN
		}
		if p.Fingerprint != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": p.Fingerprint}
		}
		if p.Security == "reality" {
			if p.PublicKey == "" {
				return nil, fmt.Errorf("в REALITY отсутствует public key")
			}
			tls["reality"] = map[string]any{"enabled": true, "public_key": p.PublicKey, "short_id": p.ShortID}
			if p.Fingerprint == "" {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
			}
		}
		out["tls"] = tls
	}
	return out, nil
}
