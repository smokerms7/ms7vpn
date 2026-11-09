package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"ms7vpn/internal/model"
	"ms7vpn/internal/xray"
)

// Пинг «как в Shade»: настоящий HTTP-запрос через каждый сервер.
//
// Прежний TCP-пинг мерил только соединение с портом. Когда в системе включён
// TUN (свой или чужой — Happ, v2rayN), соединение перехватывал адаптер и
// отвечал сам, поэтому почти все серверы показывали 1 мс. Теперь поднимается
// временный Xray с отдельным входом на каждый сервер, его сокеты привязаны к
// физическому адаптеру, и через каждый вход открывается адрес проверки.

type pingResult struct {
	id     string
	ms     int
	status string
}

func (a *App) realPing(ctx context.Context, nodes []model.Node, settings model.Settings) (map[string]pingResult, []model.Node, error) {
	ensureCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err := a.core.Ensure(ensureCtx)
	cancel()
	if err != nil {
		return nil, nodes, fmt.Errorf("ядро Xray для проверки пинга: %w", err)
	}

	// Физический адаптер, чтобы проверка шла мимо любого туннеля. Не нашли —
	// мерим как есть: лучше неточный пинг, чем никакого.
	ifaceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	iface, _ := physicalInterface(ifaceCtx)
	cancel()

	targets := make([]xray.PingTarget, 0, len(nodes))
	var listeners []net.Listener
	for _, node := range nodes {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		listeners = append(listeners, listener)
		targets = append(targets, xray.PingTarget{Node: node, Port: listener.Addr().(*net.TCPAddr).Port})
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}

	config, used, err := xray.BuildPingConfig(targets, iface, settings.DNS1)
	if err != nil {
		return nil, nodes, err
	}
	usedIDs := map[string]bool{}
	for _, target := range used {
		usedIDs[target.Node.ID] = true
	}
	var rest []model.Node
	for _, node := range nodes {
		if !usedIDs[node.ID] {
			rest = append(rest, node)
		}
	}

	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	runDir := a.core.RuntimeDir()
	_ = os.MkdirAll(runDir, 0o700)
	configPath := filepath.Join(runDir, "ping-"+hex.EncodeToString(suffix[:])+".json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		return nil, rest, err
	}
	defer os.Remove(configPath)

	cmd := exec.Command(a.core.CorePath(), "run", "-c", configPath)
	cmd.Dir = filepath.Dir(a.core.CorePath())
	xray.PrepareChild(cmd)
	if err := cmd.Start(); err != nil {
		return nil, rest, fmt.Errorf("запуск Xray для проверки пинга: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}()
	if err := xray.WaitLocalPort(ctx, used[0].Port, 6*time.Second); err != nil {
		return nil, rest, fmt.Errorf("Xray для проверки пинга не запустился")
	}

	timeout := time.Duration(settings.PingTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	testURL := settings.TestURL
	if testURL == "" {
		testURL = model.DefaultSettings().TestURL
	}
	method := http.MethodGet
	if settings.PingMode == model.PingRealHEAD {
		method = http.MethodHead
	}

	results := map[string]pingResult{}
	var mu sync.Mutex
	jobs := make(chan xray.PingTarget)
	var wg sync.WaitGroup
	workers := 16
	if len(used) < workers {
		workers = len(used)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				ms := delayThroughSOCKS(ctx, target.Port, method, testURL, timeout)
				result := pingResult{id: target.Node.ID, ms: ms, status: "ok"}
				if ms <= 0 {
					result = pingResult{id: target.Node.ID, ms: -1, status: "error"}
				}
				mu.Lock()
				results[target.Node.ID] = result
				mu.Unlock()
			}
		}()
	}
	for _, target := range used {
		select {
		case jobs <- target:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return results, rest, nil
}

// delayThroughSOCKS — время ответа адреса проверки через локальный SOCKS,
// мс; -1 — ответа нет. В цифру входит соединение с сервером, рукопожатие и
// сам запрос: столько же займёт открытие сайта через этот сервер.
func delayThroughSOCKS(ctx context.Context, port int, method, testURL string, timeout time.Duration) int {
	proxyURL, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(reqCtx, method, testURL, nil)
	if err != nil {
		return -1
	}
	request.Header.Set("User-Agent", "MS7VPN")
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return -1
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if response.StatusCode >= 500 {
		return -1
	}
	ms := int(time.Since(started) / time.Millisecond)
	if ms < 1 {
		ms = 1
	}
	return ms
}
