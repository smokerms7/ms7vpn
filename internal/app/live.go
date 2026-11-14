package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"ms7vpn/internal/model"
)

// livePingLoop раз в несколько секунд меряет задержку через туннель —
// для плитки «Пинг» на главной. Клиент держит соединение открытым, поэтому
// после первого замера в цифру входит только путь до сервера и обратно, а не
// каждый раз новое рукопожатие TLS.
func (a *App) livePingLoop() {
	// Тик частый, замер — раз в interval или сразу на новом подключении.
	const interval = 4 * time.Second
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var (
		client   *http.Client
		clientID string
		lastAt   time.Time
	)
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
		}
		snapshot := a.Snapshot()
		conn := snapshot.Connection
		if !conn.Connected || conn.Mode == model.ModeZapret {
			if client != nil {
				client.CloseIdleConnections()
				client, clientID = nil, ""
			}
			continue
		}
		a.portMu.Lock()
		httpPort := a.httpPort
		a.portMu.Unlock()

		// sing-box в TUN не открывает HTTP-вход: запрос и так идёт в туннель.
		useProxy := !(conn.Engine == "sing-box" && conn.Mode == "tun") && httpPort > 0
		id := fmt.Sprintf("%s|%s|%s|%d|%v", conn.NodeID, conn.Engine, conn.Mode, httpPort, conn.StartedAt.UnixNano())
		fresh := client == nil || id != clientID
		if !fresh && time.Since(lastAt) < interval {
			continue
		}
		if fresh {
			if client != nil {
				client.CloseIdleConnections()
			}
			transport := &http.Transport{Proxy: nil, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
			if useProxy {
				proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", httpPort))
				transport.Proxy = http.ProxyURL(proxyURL)
			}
			client = &http.Client{Transport: transport, Timeout: 6 * time.Second}
			clientID = id
		}

		testURL := snapshot.Settings.TestURL
		if testURL == "" {
			testURL = model.DefaultSettings().TestURL
		}
		if fresh {
			// Прогрев: первый запрос открывает соединение через сервер, и в
			// его цифре рукопожатия — на экране она выглядела в 3–5 раз хуже
			// настоящей. Показываем уже второй.
			measureDelay(client, testURL)
		}
		ms := measureDelay(client, testURL)
		lastAt = time.Now()

		a.mu.Lock()
		if a.state.Connection.Connected && a.state.Connection.NodeID == conn.NodeID {
			a.state.Connection.PingMS = ms
		}
		a.mu.Unlock()
		a.usage.addPing(ms)
	}
}

// measureDelay — время ответа на лёгкий запрос, мс; -1 — ответа нет.
func measureDelay(client *http.Client, testURL string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, testURL, nil)
	if err != nil {
		return -1
	}
	request.Header.Set("User-Agent", "MS7VPN")
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return -1
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
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
