//go:build ms7android

package app

// Платформа Android. Программа работает отдельным процессом внутри APK, а всё,
// что умеет только Android (VpnService, список приложений, значки, браузер,
// установка APK), делает оболочка на Kotlin. Связь с ней — «мост»: локальный
// сокет в абстрактном пространстве имён, одна строка JSON на запрос, одна на
// ответ. Вместе с ответом на "tun" оболочка передаёт дескриптор TUN
// (SCM_RIGHTS).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"ms7vpn/internal/xray"
)

// BridgeName — имя сокета оболочки (переменная MS7_BRIDGE).
var BridgeName = os.Getenv("MS7_BRIDGE")

// tunMu: запросы о TUN не должны пересекаться.
var tunMu sync.Mutex

// BridgeCall выполняет запрос к оболочке. Возвращает ответ и (если пришёл)
// дескриптор.
func BridgeCall(ctx context.Context, request map[string]any) (map[string]any, int, error) {
	if BridgeName == "" {
		return nil, -1, errors.New("нет связи с оболочкой Android")
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", "@"+BridgeName)
	if err != nil {
		return nil, -1, fmt.Errorf("оболочка Android недоступна: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	payload, _ := json.Marshal(request)
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, -1, err
	}
	unixConn := conn.(*net.UnixConn)
	fd := -1
	var line []byte
	buf := make([]byte, 64<<10)
	oob := make([]byte, syscall.CmsgSpace(4*4))
	for {
		n, oobn, _, _, err := unixConn.ReadMsgUnix(buf, oob)
		if oobn > 0 && fd < 0 {
			if messages, perr := syscall.ParseSocketControlMessage(oob[:oobn]); perr == nil {
				for _, message := range messages {
					if fds, rerr := syscall.ParseUnixRights(&message); rerr == nil && len(fds) > 0 {
						fd = fds[0]
						for _, extra := range fds[1:] {
							_ = syscall.Close(extra)
						}
					}
				}
			}
		}
		line = append(line, buf[:n]...)
		if i := strings.IndexByte(string(line), '\n'); i >= 0 {
			line = line[:i]
			break
		}
		if err != nil || n == 0 {
			if fd >= 0 {
				_ = syscall.Close(fd)
			}
			if err == nil {
				err = errors.New("оболочка закрыла соединение")
			}
			return nil, -1, err
		}
	}
	var response map[string]any
	if err := json.Unmarshal(line, &response); err != nil {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
		return nil, -1, fmt.Errorf("ответ оболочки: %w", err)
	}
	if ok, _ := response["ok"].(bool); !ok {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
		message, _ := response["error"].(string)
		if message == "" {
			message = "оболочка Android отказала"
		}
		return response, -1, errors.New(message)
	}
	return response, fd, nil
}

func bridgeSimple(op string, fields map[string]any, timeout time.Duration) (map[string]any, error) {
	request := map[string]any{"op": op}
	for k, v := range fields {
		request[k] = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	response, fd, err := BridgeCall(ctx, request)
	if fd >= 0 {
		_ = syscall.Close(fd)
	}
	return response, err
}

func init() {
	xray.TunMTU = 1500
	platformStartTun = startAndroidTun
	platformStopTun = func() {
		_, _ = bridgeSimple("tunStop", nil, 5*time.Second)
	}
}

// startAndroidTun: оболочка поднимает VpnService (при первом запуске Android
// спросит разрешение), отдаёт дескриптор — Xray получает его как fd 3.
// Само приложение и Xray исключены из туннеля, поэтому их соединения с
// сервером идут напрямую.
func startAndroidTun(a *App, ctx context.Context, p nativeTunParams) ([]string, error) {
	tunMu.Lock()
	defer tunMu.Unlock()
	t0 := time.Now()

	serverIP := ""
	if ip := net.ParseIP(p.node.Address); ip != nil {
		serverIP = ip.String()
	} else {
		resolveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		ips, err := net.DefaultResolver.LookupIP(resolveCtx, "ip4", p.node.Address)
		cancel()
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("не удалось получить адрес сервера %s — проверьте интернет", p.node.Address)
		}
		serverIP = ips[0].String()
	}

	// Настройки телефона: MTU, sniffing, доступ из локальной сети.
	xray.TunMTU = 1500
	if p.settings.TunMTU >= 1280 && p.settings.TunMTU <= 9000 {
		xray.TunMTU = p.settings.TunMTU
	}
	xray.NoSniffing = p.settings.NoSniffing
	xray.Fragment = p.settings.Fragment
	xray.Mux = p.settings.Mux
	xray.DomainStrategy = p.settings.DomainStrategy
	directRu, directDomains, proxyDomains := p.settings.DirectRu, p.settings.DirectDomains, p.settings.ProxyDomains
	if p.settings.NoRouting {
		directRu, directDomains, proxyDomains = false, nil, nil
	}
	xray.InboundListen = "127.0.0.1"
	if p.settings.AllowLAN {
		xray.InboundListen = "0.0.0.0"
	}
	built, err := xray.Build(p.node, xray.ConfigOptions{
		Mode: "tun", NativeTun: true, TunName: "tun0", ServerIP: serverIP,
		HTTPPort: p.httpPort, SOCKSPort: p.socksPort, APIPort: p.apiPort, EnableStats: true,
		DNS1: p.settings.DNS1, DNS2: p.settings.DNS2, BlockAds: p.settings.BlockAds, GeoDir: a.core.CoreDir(),
		AccessLog: a.core.RuntimeDir() + "/access.log", ErrorLog: a.core.RuntimeDir() + "/error.log",
		DirectRu: directRu, DirectDomains: directDomains, ProxyDomains: proxyDomains,
		// Приложения мимо VPN исключает сам VpnService, правило process не нужно.
	})
	if err != nil {
		return nil, err
	}

	var dns []string
	for _, server := range []string{p.settings.DNS1, p.settings.DNS2} {
		if ip := net.ParseIP(strings.TrimSpace(server)); ip != nil {
			dns = append(dns, ip.String())
		}
	}
	tunCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	_, fd, err := BridgeCall(tunCtx, map[string]any{
		"op":         "tun",
		"mtu":        xray.TunMTU,
		"dns":        dns,
		"directApps": p.settings.DirectApps,
		"perAppMode": p.settings.PerAppMode,
		"nodeName":   p.node.Name,
	})
	cancel()
	if err != nil {
		return nil, err
	}
	if fd < 0 {
		return nil, errors.New("оболочка не передала туннель")
	}
	a.logf("VpnService поднят (+%d мс)", time.Since(t0).Milliseconds())

	file := os.NewFile(uintptr(fd), "tun")
	a.core.SetTunFile(file)
	startErr := a.core.Start(built.Config, "tun")
	a.core.SetTunFile(nil)
	_ = file.Close() // у Xray своя копия
	if startErr == nil {
		startErr = a.core.WaitAlive(ctx, 400*time.Millisecond)
	}
	if startErr != nil {
		_, _ = bridgeSimple("tunStop", nil, 5*time.Second)
		return nil, startErr
	}
	a.logf("ядро Xray с TUN запущено (+%d мс)", time.Since(t0).Milliseconds())
	if warmUp(ctx, p.httpPort, 6*time.Second) {
		a.logf("канал проверен, трафик идёт (+%d мс)", time.Since(t0).Milliseconds())
	} else {
		a.logf("канал не ответил за 6 с — продолжаю")
	}
	return built.Warnings, nil
}

// ==== то, что на Windows делают platform_windows.go и extras_windows.go ====

func IsAdministrator() bool                      { return true }
func RelaunchElevated(_ string) error            { return errors.New("на Android не нужно") }
func EnableSystemProxy(_ string, _, _ int) error { return nil }
func RestoreSystemProxy(_ string) error          { return nil }
func RecoverSystemProxy(_ string) error          { return nil }

// RunInstaller открывает установщик Android для скачанного APK.
func RunInstaller(path string) error {
	_, err := bridgeSimple("install", map[string]any{"path": path}, 20*time.Second)
	return err
}

func OpenAppWindow(rawURL, _ string) (<-chan struct{}, error) { return nil, OpenExternalURL(rawURL) }

func OpenFolder(_ string) error {
	return errors.New("на Android папка с данными недоступна")
}

func OpenExternalURL(rawURL string) error {
	_, err := bridgeSimple("open", map[string]any{"url": rawURL}, 10*time.Second)
	return err
}

const AutostartArg = "--autostart"

func AutostartEnabled() bool {
	response, err := bridgeSimple("autostart", nil, 5*time.Second)
	if err != nil {
		return false
	}
	enabled, _ := response["enabled"].(bool)
	return enabled
}

func SetAutostart(enabled bool) error {
	_, err := bridgeSimple("autostart", map[string]any{"set": true, "enabled": enabled}, 5*time.Second)
	return err
}

func RegisterURLScheme() error             { return nil }
func EnableConsoleProxy(string, int) error { return nil }
func RestoreConsoleProxy(string) error     { return nil }

// AndroidApp — установленное приложение.
type AndroidApp struct {
	Package string `json:"pkg"`
	Label   string `json:"label"`
}

// InstalledApps — приложения с доступом в интернет, кроме самого MS7.
func InstalledApps() []AndroidApp {
	response, err := bridgeSimple("apps", nil, 20*time.Second)
	if err != nil {
		return nil
	}
	raw, _ := json.Marshal(response["apps"])
	var apps []AndroidApp
	_ = json.Unmarshal(raw, &apps)
	return apps
}

func runningPrograms() []string {
	var names []string
	for _, app := range InstalledApps() {
		names = append(names, app.Package)
	}
	return names
}

func appIconPNG(name string) []byte {
	response, err := bridgeSimple("icon", map[string]any{"pkg": name}, 5*time.Second)
	if err != nil {
		return nil
	}
	encoded, _ := response["png"].(string)
	data, _ := base64.StdEncoding.DecodeString(encoded)
	return data
}

