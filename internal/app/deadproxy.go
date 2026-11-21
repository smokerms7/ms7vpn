package app

import (
	"net"
	"net/url"
	"strings"
	"time"
)

// deadLoopbackProxy — true, если все адреса в строке прокси локальные
// (127.0.0.1, localhost, ::1) и ни один порт не принимает соединение.
//
// Зачем: при подключении другие VPN (Happ и т. п.) останавливаются, а их
// системный прокси 127.0.0.1:10809 остаётся. Мы честно сохраняли его как
// «настройку пользователя» и после отключения возвращали — прокси указывал
// в пустоту, и интернет пропадал совсем. Мёртвый локальный прокси
// восстанавливать нельзя. Чужой удалённый или живой локальный — не трогаем.
//
// Понимает форматы «127.0.0.1:10809», «http=127.0.0.1:10809;https=…»,
// «http://127.0.0.1:10809».
func deadLoopbackProxy(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	var addrs []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ' ' }) {
		if i := strings.IndexByte(part, '='); i >= 0 && !strings.Contains(part[:i], "/") {
			part = part[i+1:]
		}
		if strings.Contains(part, "://") {
			parsed, err := url.Parse(part)
			if err != nil {
				return false
			}
			part = parsed.Host
		}
		host, port, err := net.SplitHostPort(part)
		if err != nil || port == "" {
			return false
		}
		if !isLoopbackName(host) {
			return false
		}
		addrs = append(addrs, net.JoinHostPort(host, port))
	}
	if len(addrs) == 0 {
		return false
	}
	for _, addr := range addrs {
		if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			_ = conn.Close()
			return false
		}
	}
	return true
}

func isLoopbackName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
