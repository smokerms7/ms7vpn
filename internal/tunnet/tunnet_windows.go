//go:build windows

// Package tunnet настраивает сеть Windows для TUN-режима Xray — по образцу
// Shade (route_bypass.rs).
//
// Всё делается короткими вызовами route.exe и netsh (~15 мс каждый) вместо
// PowerShell, у которого один только запуск занимает около секунды. Именно
// PowerShell-проверки делали подключение долгим: каждая итерация ожидания
// TUN стоила секунду-две.
//
// Порядок:
//  1. дождаться, пока исчезнет адаптер прошлого сеанса (иначе ядро падает с
//     «Cannot create a file when that file already exists»);
//  2. проложить маршруты /32 до адресов VPN-сервера через настоящий шлюз —
//     до того как туннель заберёт маршрут по умолчанию;
//  3. после старта ядра — DNS и метрика адаптера, маршруты 0.0.0.0/1 и
//     128.0.0.0/1 в туннель, сброс кешей DNS и маршрутов.
package tunnet

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

func runTool(exe string, args ...string) (string, error) {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.Output()
	return string(out), err
}

// runChecked — как runTool, но ошибкой считается и ненулевой код выхода:
// route.exe пишет отказ в stdout и выходит с ошибкой.
func runChecked(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("%s: %s", exe, detail)
}

// DefaultRoute — настоящий маршрут по умолчанию: шлюз и адрес адаптера, по
// данным `route print -4`. Строки таблицы — одни числа, поэтому разбор не
// зависит от языка Windows. Маршруты туннелей (on-link, без шлюза)
// отбрасываются; выигрывает меньшая метрика.
func DefaultRoute() (gateway, ifaceIP string, ok bool) {
	out, err := runTool("route", "print", "-4")
	if err != nil {
		return "", "", false
	}
	best := -1
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "0.0.0.0" || f[1] != "0.0.0.0" {
			continue
		}
		if net.ParseIP(f[2]) == nil || net.ParseIP(f[3]) == nil {
			continue
		}
		var metric int
		if _, err := fmt.Sscan(f[4], &metric); err != nil {
			continue
		}
		if best < 0 || metric < best {
			best, gateway, ifaceIP = metric, f[2], f[3]
		}
	}
	return gateway, ifaceIP, best >= 0
}

// ifaceIndexForIP — номер интерфейса с адресом ip (из netsh). Маршруты в
// обход туннеля привязываются к нему явно: пока остаётся адаптер прошлого
// сеанса, route.exe может выбрать не тот интерфейс.
func ifaceIndexForIP(ip string) string {
	out, err := runTool("netsh", "interface", "ipv4", "show", "ipaddresses")
	if err != nil {
		return ""
	}
	current := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if colon := strings.Index(line, ":"); colon > 0 {
			fields := strings.Fields(line[:colon])
			if len(fields) > 0 && isDigits(fields[len(fields)-1]) {
				current = fields[len(fields)-1]
				continue
			}
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[len(fields)-1] == ip {
			return current
		}
	}
	return ""
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ResolveIPv4All — все A-записи адреса сервера. CDN отдаёт пул адресов, и
// маршрут нужен к каждому: иначе часть соединений ядра ушла бы в собственный
// туннель. Частные и локальные адреса отбрасываются.
func ResolveIPv4All(ctx context.Context, host string) []string {
	routable := func(ip net.IP) bool {
		return ip != nil && ip.To4() != nil && !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified())
	}
	if ip := net.ParseIP(host); ip != nil {
		if routable(ip) {
			return []string{ip.To4().String()}
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, ip := range ips {
		if routable(ip) && !seen[ip.String()] {
			seen[ip.String()] = true
			out = append(out, ip.To4().String())
		}
	}
	return out
}

// adapterListed — есть ли адаптер с таким именем в выводе netsh. Имя стоит в
// конце строки, заголовки переведены, а имя — наше и не переводится.
func adapterListed(args []string, name string) bool {
	out, err := runTool("netsh", args...)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), " "+name) {
			return true
		}
	}
	return false
}

func AdapterExists(name string) bool {
	return adapterListed([]string{"interface", "show", "interface"}, name)
}

// AdapterReady — адаптер появился в IPv4-стеке, с ним можно работать.
func AdapterReady(name string) bool { return adapterIPv4Ready(name) }

func adapterIPv4Ready(name string) bool {
	return adapterListed([]string{"interface", "ipv4", "show", "interfaces"}, name)
}

// WaitAdapterGone ждёт, пока адаптер прошлого ядра исчезнет. true — его нет.
func WaitAdapterGone(name string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for AdapterExists(name) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
	return true
}

func addBypassRoute(ip, gateway, index string) error {
	_, _ = runTool("route", "delete", ip, "mask", "255.255.255.255")
	args := []string{"add", ip, "mask", "255.255.255.255", gateway, "metric", "1"}
	if index != "" {
		args = append(args, "if", index)
	}
	return runChecked("route", args...)
}

// PrepareForConnect — шаги до старта ядра: ждёт исчезновения старого
// адаптера, убирает старые маршруты /1 и прокладывает /32 до адресов
// сервера через настоящий шлюз. Возвращает адрес физического адаптера — к
// нему привязывается прямой выход Xray.
func PrepareForConnect(adapter string, bypassIPs []string) (string, error) {
	WaitAdapterGone(adapter, 15*time.Second)
	_, _ = runTool("route", "delete", "0.0.0.0", "mask", "128.0.0.0")
	_, _ = runTool("route", "delete", "128.0.0.0", "mask", "128.0.0.0")
	gateway, ifaceIP, ok := DefaultRoute()
	if !ok {
		return "", fmt.Errorf("не найден основной шлюз — проверьте подключение к интернету")
	}
	index := ifaceIndexForIP(ifaceIP)
	var errs []string
	for _, ip := range bypassIPs {
		if err := addBypassRoute(ip, gateway, index); err != nil {
			errs = append(errs, ip+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return ifaceIP, fmt.Errorf("маршрут в обход туннеля не добавлен (%s)", strings.Join(errs, "; "))
	}
	return ifaceIP, nil
}

// ConfigureAdapter — после старта ядра: ждёт адаптер, ставит DNS и метрику,
// добавляет 0.0.0.0/1 и 128.0.0.0/1 в туннель и сбрасывает кеши. Маршруты
// проверяются: без них трафик пошёл бы мимо VPN с настоящим адресом.
func ConfigureAdapter(ctx context.Context, adapter, dns string) error {
	ready := false
	for i := 0; i < 240; i++ {
		if adapterIPv4Ready(adapter) {
			ready = true
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		return fmt.Errorf("сетевой адаптер туннеля не появился")
	}
	if dns == "" {
		dns = "1.1.1.1"
	}
	_, _ = runTool("netsh", "interface", "ipv4", "set", "dnsservers", "name="+adapter, "static", dns, "primary", "validate=no")
	_, _ = runTool("netsh", "interface", "ipv4", "set", "interface", adapter, "metric=1")
	var errs []string
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		_, _ = runTool("netsh", "interface", "ipv4", "delete", "route", prefix, adapter)
		var last error
		for attempt := 0; attempt < 10; attempt++ {
			last = runChecked("netsh", "interface", "ipv4", "add", "route", prefix, adapter, "metric=1", "store=active")
			if last == nil {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if last != nil {
			errs = append(errs, prefix+" → "+last.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("не удалось добавить маршруты туннеля: %s", strings.Join(errs, "; "))
	}
	FlushCaches()
	return nil
}

// FlushCaches сбрасывает кеш DNS и кеш назначений: без этого первые секунды
// после смены маршрутов Windows ходит по старым ответам и путям.
func FlushCaches() {
	_, _ = runTool("ipconfig", "/flushdns")
	_, _ = runTool("netsh", "interface", "ip", "delete", "destinationcache")
}

// Cleanup снимает всё, что поставил сеанс: маршруты /1, обходные /32, кеши.
func Cleanup(bypassIPs []string) {
	_, _ = runTool("route", "delete", "0.0.0.0", "mask", "128.0.0.0")
	_, _ = runTool("route", "delete", "128.0.0.0", "mask", "128.0.0.0")
	for _, ip := range bypassIPs {
		_, _ = runTool("route", "delete", ip, "mask", "255.255.255.255")
	}
	FlushCaches()
}
