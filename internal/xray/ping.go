package xray

import (
	"encoding/json"
	"fmt"

	"ms7vpn/internal/model"
	"ms7vpn/internal/subscription"
)

// PingTarget — сервер и локальный SOCKS-порт, через который его меряют.
type PingTarget struct {
	Node model.Node
	Port int
}

// BuildPingConfig собирает конфиг временного Xray для проверки пинга, как в
// Shade: на каждый сервер свой локальный SOCKS-вход и свой выход, правило
// маршрутизации связывает их один к одному. Так один процесс меряет сразу
// все серверы, а запрос через порт N гарантированно идёт через сервер N.
//
// bindInterface — физический адаптер. Сокеты к серверам и DNS привязываются к
// нему, иначе при включённом TUN (своём или чужом, например Happ) проверка
// уходила бы в туннель и показывала 1 мс — время ответа самого адаптера.
//
// Возвращает конфиг и цели, которые удалось собрать: серверы, которые Xray
// не умеет (Hysteria2), в проверку не попадают.
func BuildPingConfig(targets []PingTarget, bindInterface, dns string) ([]byte, []PingTarget, error) {
	if dns == "" {
		dns = "1.1.1.1"
	}
	sockopt := func() map[string]any {
		opt := map[string]any{"domainStrategy": "UseIPv4"}
		if bindInterface != "" {
			opt["interface"] = bindInterface
		}
		return opt
	}
	inbounds := []any{}
	outbounds := []any{}
	rules := []any{
		// Запросы встроенного DNS идут прямо через физический адаптер.
		map[string]any{"type": "field", "inboundTag": []string{"dns-in"}, "outboundTag": "direct"},
	}
	var used []PingTarget
	for i, target := range targets {
		_, parsed, err := subscription.ParseURI(target.Node.RawURI)
		if err != nil || parsed.Protocol == "hysteria2" || !target.Node.Supported {
			continue
		}
		outbound, err := buildOutbound(parsed)
		if err != nil {
			continue
		}
		outTag := fmt.Sprintf("p%d", i)
		inTag := fmt.Sprintf("in%d", i)
		outbound["tag"] = outTag
		stream, _ := outbound["streamSettings"].(map[string]any)
		if stream == nil {
			stream = map[string]any{}
			outbound["streamSettings"] = stream
		}
		stream["sockopt"] = sockopt()
		outbounds = append(outbounds, outbound)
		inbounds = append(inbounds, map[string]any{
			"tag": inTag, "listen": "127.0.0.1", "port": target.Port, "protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": false},
		})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{inTag}, "outboundTag": outTag})
		used = append(used, target)
	}
	if len(used) == 0 {
		return nil, nil, fmt.Errorf("нет серверов, которые можно проверить через Xray")
	}
	outbounds = append(outbounds, map[string]any{
		"tag": "direct", "protocol": "freedom",
		"settings":       map[string]any{"domainStrategy": "UseIPv4"},
		"streamSettings": map[string]any{"sockopt": sockopt()},
	})
	config := map[string]any{
		"log":       map[string]any{"loglevel": "none"},
		"dns":       map[string]any{"tag": "dns-in", "servers": []string{dns, "8.8.8.8"}, "queryStrategy": "UseIPv4"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   map[string]any{"domainStrategy": "AsIs", "rules": rules},
	}
	data, err := json.Marshal(config)
	return data, used, err
}
