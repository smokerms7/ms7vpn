package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ms7vpn/internal/model"
	"ms7vpn/internal/tunnet"
	"ms7vpn/internal/xray"
)

// TUN самого Xray — как в Shade. Раньше TUN держал sing-box, а Xray стоял за
// ним SOCKS-сервером: два процесса, и проверки сети через PowerShell, по
// секунде-две каждая. Отсюда долгое подключение и «залипший» адаптер после
// неудачи. Теперь: один процесс Xray с входом tun, маршруты и DNS — быстрыми
// route/netsh (пакет tunnet).

func tunBypassPath(dataDir string) string { return filepath.Join(dataDir, "tun-bypass.json") }

// saveTunBypass запоминает обходные маршруты на диск: если программу убьют,
// при следующем запуске они будут сняты.
func (a *App) saveTunBypass(ips []string) {
	a.tunMu.Lock()
	a.tunBypass = append([]string(nil), ips...)
	a.tunMu.Unlock()
	data, _ := json.Marshal(ips)
	_ = os.WriteFile(tunBypassPath(a.dataDir), data, 0o600)
}

// cleanupNativeTun снимает маршруты сеанса. Безопасно вызывать всегда.
func (a *App) cleanupNativeTun() {
	a.tunMu.Lock()
	ips := a.tunBypass
	a.tunBypass = nil
	a.tunMu.Unlock()
	if ips == nil {
		return
	}
	tunnet.Cleanup(ips)
	_ = os.Remove(tunBypassPath(a.dataDir))
}

// RecoverNativeTun — после сбоя: снять маршруты прошлого сеанса.
func RecoverNativeTun(dataDir string) {
	data, err := os.ReadFile(tunBypassPath(dataDir))
	if err != nil {
		return
	}
	var ips []string
	_ = json.Unmarshal(data, &ips)
	tunnet.Cleanup(ips)
	_ = os.Remove(tunBypassPath(dataDir))
}

// ensureWintun кладёт wintun.dll рядом с xray.exe: ядро грузит драйвер из
// своей папки.
func (a *App) ensureWintun() {
	if a.core.HasWintun() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	for _, candidate := range []string{
		filepath.Join(filepath.Dir(exe), "wintun.dll"),
		filepath.Join(filepath.Dir(exe), "bundle", "wintun.dll"),
	} {
		if data, err := os.ReadFile(candidate); err == nil {
			_ = os.WriteFile(filepath.Join(a.core.CoreDir(), "wintun.dll"), data, 0o644)
			return
		}
	}
}

func transientTunStart(err error) bool {
	text := err.Error()
	for _, mark := range []string{"already exists", "уже существует", "Element not found", "Элемент не найден", "unable to set routes"} {
		if strings.Contains(text, mark) {
			return true
		}
	}
	return false
}

type nativeTunParams struct {
	node                         model.Node
	settings                     model.Settings
	httpPort, socksPort, apiPort int
}

// startNativeTun поднимает туннель. Возвращает предупреждения для окна.
func (a *App) startNativeTun(ctx context.Context, p nativeTunParams) ([]string, error) {
	t0 := time.Now()
	ips := tunnet.ResolveIPv4All(ctx, p.node.Address)
	if len(ips) == 0 {
		return nil, fmt.Errorf("не удалось получить адрес сервера %s — проверьте интернет", p.node.Address)
	}
	sendThrough, err := tunnet.PrepareForConnect(xray.TunAdapterName, ips)
	for _, name := range xray.TunAdapterNames[1:] {
		tunnet.WaitAdapterGone(name, 3*time.Second)
	}
	a.saveTunBypass(ips)
	if err != nil {
		return nil, err
	}
	a.logf("сеть подготовлена: маршрутов в обход туннеля %d (+%d мс)", len(ips), time.Since(t0).Milliseconds())

	a.ensureWintun()
	build := func(name string) (xray.BuildResult, error) {
		return xray.Build(p.node, xray.ConfigOptions{
			Mode: "tun", NativeTun: true, TunName: name, SendThrough: sendThrough, ServerIP: ips[0],
			HTTPPort: p.httpPort, SOCKSPort: p.socksPort, APIPort: p.apiPort, EnableStats: true,
			DNS1: p.settings.DNS1, DNS2: p.settings.DNS2, BlockAds: p.settings.BlockAds, GeoDir: a.core.CoreDir(),
			AccessLog: filepath.Join(a.core.RuntimeDir(), "access.log"), ErrorLog: filepath.Join(a.core.RuntimeDir(), "error.log"),
			DirectRu: p.settings.DirectRu, DirectDomains: p.settings.DirectDomains, ProxyDomains: p.settings.ProxyDomains,
			DirectApps: p.settings.DirectApps,
		})
	}
	adapter := xray.TunAdapterName
	built, err := build(adapter)
	if err != nil {
		return nil, err
	}

	// Ядро иногда падает на старте, пока Windows не отпустила адаптер
	// прошлого сеанса. Первый повтор — с тем же именем, дальше — с запасными:
	// «занятое» имя может не освободиться до перезагрузки.
	for attempt := 0; ; attempt++ {
		startErr := a.core.Start(built.Config, "tun")
		if startErr == nil {
			startErr = a.waitTunAdapter(ctx, adapter, 4*time.Second)
		}
		if startErr == nil {
			break
		}
		_ = a.core.Stop()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Повторы: то же имя, затем каждое запасное по разу.
		if attempt < len(xray.TunAdapterNames) && transientTunStart(startErr) {
			tunnet.WaitAdapterGone(adapter, 3*time.Second)
			if attempt >= 1 {
				adapter = xray.TunAdapterNames[attempt]
				var buildErr error
				if built, buildErr = build(adapter); buildErr != nil {
					return nil, buildErr
				}
			}
			a.logf("адаптер не создался (%s) — повтор %d, имя %s", firstLine(startErr.Error()), attempt+1, adapter)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		return nil, startErr
	}
	a.logf("ядро Xray с TUN запущено (+%d мс)", time.Since(t0).Milliseconds())

	// Прогрев: канал до сервера через локальный HTTP-вход. Короткие попытки
	// каждые 120 мс, не дольше 6 секунд. Не ответил — всё равно включаем
	// маршруты, первые секунды могут быть медленнее.
	if warmUp(ctx, p.httpPort, 6*time.Second) {
		a.logf("канал проверен, трафик идёт (+%d мс)", time.Since(t0).Milliseconds())
	} else {
		a.logf("канал не ответил за 6 с — включаю маршрутизацию не дожидаясь")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := tunnet.ConfigureAdapter(ctx, adapter, p.settings.DNS1); err != nil {
		return nil, fmt.Errorf("%w. Подключение отменено, чтобы трафик не пошёл мимо VPN", err)
	}
	a.logf("маршрутизация и DNS адаптера настроены (итого %d мс)", time.Since(t0).Milliseconds())
	return built.Warnings, nil
}

// waitTunAdapter ждёт, пока Xray создаст адаптер. Обычно это 0,3 с. Если
// ядро упало — возвращает его ошибку; если зависло на создании (так бывает с
// «занятым» именем) — ошибку «already exists», чтобы перейти к запасному имени.
func (a *App) waitTunAdapter(ctx context.Context, name string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if tunnet.AdapterReady(name) {
			return nil
		}
		if !a.core.Running() {
			return fmt.Errorf("Xray завершился при создании адаптера: %s", a.core.TailLog(8))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	if log := a.core.TailLog(8); transientTunStart(fmt.Errorf("%s", log)) {
		return fmt.Errorf("%s", log)
	}
	return fmt.Errorf("адаптер %s не появился за %s: already exists", name, limit)
}

func warmUp(ctx context.Context, httpPort int, budget time.Duration) bool {
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", httpPort))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 1500 * time.Millisecond}
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://www.gstatic.com/generate_204", nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode < 400 {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(120 * time.Millisecond):
		}
	}
	return false
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}
