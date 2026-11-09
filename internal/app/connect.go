package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"ms7vpn/internal/model"
	"ms7vpn/internal/singbox"
	"ms7vpn/internal/tunnet"
	"ms7vpn/internal/xray"
)

func (a *App) stopVPN() error {
	err := errors.Join(a.box.Stop(), a.core.Stop())
	a.cleanupNativeTun()
	return err
}
func (a *App) vpnRunning() bool {
	c := a.Snapshot().Connection
	if c.Engine == "sing-box" {
		return a.box.Running()
	}
	// TUN на ядре Xray — один процесс Xray, sing-box не участвует.
	return a.core.Running()
}

func (a *App) Connect(parent context.Context, nodeID, mode string) (model.AppState, error) {
	if a.Snapshot().Settings.Mode == model.ModeZapret {
		return a.EnableZapret(parent)
	}
	if !a.connectMu.TryLock() {
		return a.Snapshot(), &APIError{Code: "BUSY", Message: "Подключение уже выполняется"}
	}
	defer a.connectMu.Unlock()
	ctx, finish := a.beginConnect(parent)
	defer finish()
	settings := a.Snapshot().Settings
	if mode == "" {
		mode = settings.ConnectionMode
	}
	if mode != "tun" && mode != "system-proxy" {
		return a.Snapshot(), &APIError{Code: "INVALID_MODE", Message: "Неизвестный режим подключения"}
	}
	// Never silently downgrade a requested full tunnel to an application proxy.
	if mode == "tun" && !IsAdministrator() {
		return a.Snapshot(), &APIError{Code: "NEED_ADMIN", Message: "Для TUN нужен запуск от имени администратора"}
	}
	a.mu.RLock()
	if nodeID == "" {
		nodeID = a.state.SelectedNodeID
	}
	node, subID, found := a.findNodeLocked(nodeID)
	a.mu.RUnlock()
	if !found {
		return a.Snapshot(), &APIError{Code: "NODE_NOT_FOUND", Message: "Сначала выберите сервер"}
	}
	if settings.Engine == "sing-box" {
		if _, err := singbox.Outbound(node); err != nil {
			return a.Snapshot(), &APIError{Code: "NODE_UNSUPPORTED", Message: err.Error()}
		}
	} else {
		if _, err := xray.Build(node, xray.ConfigOptions{Mode: "system-proxy"}); err != nil {
			return a.Snapshot(), &APIError{Code: "NODE_UNSUPPORTED", Message: err.Error()}
		}
	}

	if err := a.stopVPN(); err != nil {
		return a.Snapshot(), err
	}
	if err := RestoreSystemProxy(a.dataDir); err != nil {
		return a.Snapshot(), err
	}
	_ = RestoreConsoleProxy(a.dataDir)
	a.statsMu.Lock()
	a.statsClient = nil
	a.statsMu.Unlock()
	a.mu.Lock()
	a.state.Connection = model.ConnectionState{Connecting: true, NodeID: node.ID, NodeName: node.Name, Mode: mode, Engine: settings.Engine, StatusMessage: "Подготовка ядра VPN…"}
	a.mu.Unlock()
	a.logf("подключение: сервер %q, ядро %s, режим %s", node.Name, settings.Engine, mode)
	boxStarted := false
	// TUN с ядром Xray — встроенный TUN Xray, как в Shade (tun_native.go).
	// sing-box для этого больше не нужен.
	nativeTun := mode == "tun" && settings.Engine == "xray"
	fail := func(err error) (model.AppState, error) {
		if settings.Engine == "sing-box" && boxStarted && strings.Contains(a.box.TailLog(20), "reality verification failed") {
			err = fmt.Errorf("sing-box не прошёл проверку REALITY этого сервера. Выберите ядро Xray или сервер TLS/Hysteria2. Проверка подлинности не отключалась: %w", err)
		}
		a.logf("ОШИБКА подключения: %v", err)
		_ = a.stopVPN()
		a.cleanupNativeTun()
		_ = RestoreSystemProxy(a.dataDir)
		_ = RestoreConsoleProxy(a.dataDir)
		a.mu.Lock()
		a.state.Connection = model.ConnectionState{Mode: mode, Engine: settings.Engine, LastError: err.Error(), StatusMessage: "Ошибка подключения"}
		state := a.state
		a.mu.Unlock()
		return state, &APIError{Code: "CONNECT_FAILED", Message: err.Error()}
	}
	// Fetch required cores while the previous VPN still provides connectivity.
	ensureCtx, ensureCancel := context.WithTimeout(ctx, 9*time.Minute)
	var ensureErr error
	if settings.Engine == "xray" {
		ensureErr = a.core.Ensure(ensureCtx)
	}
	if ensureErr == nil && (settings.Engine == "sing-box" || (mode == "tun" && !nativeTun)) {
		ensureErr = a.box.Ensure(ensureCtx)
	}
	ensureCancel()
	if ensureErr != nil {
		return fail(ensureErr)
	}
	conflictCtx, conflictCancel := context.WithTimeout(ctx, 30*time.Second)
	stoppedVPNs, conflictErr := stopConflictingVPNs(conflictCtx, []string{a.core.CorePath(), a.box.CorePath()})
	conflictCancel()
	if len(stoppedVPNs) > 0 {
		a.logf("отключены другие VPN: %s", strings.Join(stoppedVPNs, ", "))
	}
	if conflictErr != nil {
		state, err := fail(conflictErr)
		if errors.Is(conflictErr, errVPNConflictNeedsAdmin) {
			err.(*APIError).Code = "NEED_ADMIN"
		}
		return state, err
	}
	var iface, serverIP string
	if mode == "tun" && !nativeTun {
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		var err error
		iface, err = tunEnvironment(checkCtx)
		cancel()
		if err != nil {
			return fail(err)
		}
		if settings.Engine == "xray" {
			if ip := net.ParseIP(node.Address); ip != nil {
				serverIP = ip.String()
			} else {
				resolveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				ips, err := net.DefaultResolver.LookupIP(resolveCtx, "ip4", node.Address)
				cancel()
				if err != nil || len(ips) == 0 {
					return fail(errors.New("Не удалось разрешить адрес VPN-сервера до запуска TUN"))
				}
				serverIP = ips[0].String()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	httpPort, socksPort := proxyPorts(xray.FreeLocalPort)
	a.logf("локальные порты: HTTP=%d, SOCKS5=%d", httpPort, socksPort)
	apiPort := xray.FreeLocalPort(10085)
	if apiPort == httpPort || apiPort == socksPort {
		apiPort = xray.FreeLocalPort(socksPort + 1)
	}
	var warnings []string
	if len(stoppedVPNs) > 0 {
		warnings = append(warnings, "Отключены другие VPN: "+strings.Join(stoppedVPNs, ", "))
	}
	if mode == "system-proxy" && (httpPort != 10809 || socksPort != 10808) {
		warnings = append(warnings, fmt.Sprintf("Стандартные порты заняты. HTTP: 127.0.0.1:%d; SOCKS5: 127.0.0.1:%d. Если в приложении задан прокси вручную, укажите этот порт.", httpPort, socksPort))
	}
	if nativeTun {
		tunWarnings, err := a.startNativeTun(ctx, nativeTunParams{node: node, settings: settings, httpPort: httpPort, socksPort: socksPort, apiPort: apiPort})
		if err != nil {
			return fail(err)
		}
		warnings = append(warnings, tunWarnings...)
		a.statsMu.Lock()
		a.statsClient = xray.NewStatsClient(apiPort)
		a.statsMu.Unlock()
	} else if settings.Engine == "xray" {
		ensureCtx, cancel := context.WithTimeout(ctx, 9*time.Minute)
		err := a.core.Ensure(ensureCtx)
		cancel()
		if err != nil {
			return fail(err)
		}
		built, err := xray.Build(node, xray.ConfigOptions{Mode: "system-proxy", HTTPPort: httpPort, SOCKSPort: socksPort, APIPort: apiPort, EnableStats: true, DNS1: settings.DNS1, DNS2: settings.DNS2, BlockAds: settings.BlockAds, GeoDir: a.core.CoreDir(), AccessLog: filepath.Join(a.core.RuntimeDir(), "access.log"), ErrorLog: filepath.Join(a.core.RuntimeDir(), "error.log"), BindInterface: iface, ServerIP: serverIP, DirectRu: settings.DirectRu, DirectDomains: settings.DirectDomains, ProxyDomains: settings.ProxyDomains})
		if err != nil {
			return fail(err)
		}
		warnings = append(warnings, built.Warnings...)
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := a.core.Start(built.Config, "system-proxy"); err != nil {
			return fail(err)
		}
		if err := a.core.WaitAlive(ctx, 400*time.Millisecond); err != nil {
			return fail(err)
		}
		if mode == "system-proxy" {
			a.statsMu.Lock()
			a.statsClient = xray.NewStatsClient(apiPort)
			a.statsMu.Unlock()
		}
	}
	if settings.Engine == "sing-box" || (mode == "tun" && !nativeTun) {
		// Адаптер прошлого неудачного сеанса: без ожидания sing-box падает
		// с «Cannot create a file when that file already exists».
		tunnet.WaitAdapterGone(singbox.TunName, 15*time.Second)
		ensureCtx, cancel := context.WithTimeout(ctx, 9*time.Minute)
		err := a.box.Ensure(ensureCtx)
		cancel()
		if err != nil {
			return fail(err)
		}
		// The control API is local and authenticated, including when a browser runs.
		var secretBytes [32]byte
		if _, err := rand.Read(secretBytes[:]); err != nil {
			return fail(err)
		}
		secret := hex.EncodeToString(secretBytes[:])
		boxPort := xray.FreeLocalPort(apiPort + 1)
		if boxPort == httpPort || boxPort == socksPort {
			boxPort = xray.FreeLocalPort(socksPort + 2)
		}
		o := singbox.Options{Mode: mode, HTTPPort: httpPort, SOCKSPort: socksPort, APIPort: boxPort, Secret: secret, DNS1: settings.DNS1, DNS2: settings.DNS2, Interface: iface, BlockAds: settings.BlockAds, DirectRu: settings.DirectRu, DirectDomains: settings.DirectDomains, ProxyDomains: settings.ProxyDomains, DirectApps: settings.DirectApps}
		if settings.Engine == "xray" {
			o.XraySOCKSPort = socksPort
			warnings = append(warnings, "TUN: sing-box; соединение с VPN-сервером: Xray")
		}
		config, err := singbox.Build(node, o)
		if err != nil {
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := a.box.Start(ctx, config); err != nil {
			return fail(err)
		}
		boxStarted = true
		if err := a.box.WaitAlive(ctx); err != nil {
			return fail(err)
		}
		a.statsMu.Lock()
		a.statsClient = singbox.NewStatsClient(boxPort, secret)
		a.statsMu.Unlock()
		if settings.BlockAds {
			warnings = append(warnings, "sing-box использует встроенный базовый список рекламных доменов")
		}
	}
	if mode == "tun" && !nativeTun {
		if err := a.waitTunReady(ctx, settings.TestURL); err != nil {
			return fail(err)
		}
	} else {
		checkCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
		err := xray.VerifyHTTPProxy(checkCtx, httpPort, settings.TestURL)
		cancel()
		if err != nil {
			return fail(fmt.Errorf("проверка через %s: %w", settings.Engine, err))
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := EnableSystemProxy(a.dataDir, httpPort, socksPort); err != nil {
			return fail(err)
		}
		if settings.ConsoleProxy {
			if err := EnableConsoleProxy(a.dataDir, httpPort); err != nil {
				a.logf("не удалось задать прокси для консольных программ: %v", err)
			} else {
				a.logf("прокси для консольных программ: http://127.0.0.1:%d", httpPort)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	a.portMu.Lock()
	a.httpPort = httpPort
	a.socksPort = socksPort
	a.portMu.Unlock()
	a.mu.Lock()
	a.recordConnection()
	a.state.Connection = model.ConnectionState{Connected: true, NodeID: node.ID, NodeName: node.Name, Mode: mode, Engine: settings.Engine, StartedAt: time.Now(), StatusMessage: "Подключено", Warnings: warnings}
	if mode == "system-proxy" {
		a.state.Connection.LocalHTTPProxy = fmt.Sprintf("127.0.0.1:%d", httpPort)
		a.state.Connection.LocalSOCKS = fmt.Sprintf("127.0.0.1:%d", socksPort)
	}
	a.state.SelectedNodeID = node.ID
	a.state.SelectedSubID = subID
	a.state.RecentNodeIDs = prependUnique(a.state.RecentNodeIDs, node.ID, 20)
	saveErr := a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	if saveErr != nil {
		return fail(saveErr)
	}
	a.logf("подключено: %s (%s, %s)", node.Name, settings.Engine, mode)
	return state, nil
}

// Preserve the ports used before the dual-core update. Applications can have
// a manually configured SOCKS endpoint even when browsers use Windows proxy.
func proxyPorts(available func(int) int) (httpPort, socksPort int) {
	httpPort = available(10809)
	socksPort = available(10808)
	if socksPort == httpPort {
		socksPort = available(httpPort + 1)
	}
	return
}

func (a *App) waitTunReady(ctx context.Context, testURL string) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !a.vpnRunning() {
			return fmt.Errorf("ядро остановилось при запуске TUN: %s", a.box.TailLog(12))
		}
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		lastErr = verifyTunRoutes(probeCtx)
		if lastErr == nil {
			lastErr = xray.VerifyTUNPath(probeCtx, testURL, singbox.TunIPv4)
		}
		cancel()
		if lastErr == nil {
			a.logf("проверен TUN %s: маршруты IPv4 и контрольный запрос с адреса адаптера", singbox.TunName)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("TUN не прошёл проверку: %w", lastErr)
}
