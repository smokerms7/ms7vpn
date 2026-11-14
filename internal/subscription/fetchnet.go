package subscription

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// DialFunc открывает соединение мимо туннеля (через физический адаптер).
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Задержка, после которой к прямой попытке добавляется обычная — через
// туннель. Прямой путь отвечает за 0,3–0,5 с; если за это время ответа нет,
// он, скорее всего, закрыт провайдером.
const directHeadStart = 1200 * time.Millisecond

// Хост, до которого прямой путь не дошёл, полчаса сразу идёт через туннель.
const directBlockedFor = 30 * time.Minute

var (
	directMu      sync.Mutex
	directBlocked = map[string]time.Time{}
)

func directAllowed(host string) bool {
	directMu.Lock()
	defer directMu.Unlock()
	until, ok := directBlocked[host]
	return !ok || time.Now().After(until)
}

func markDirect(host string, works bool) {
	directMu.Lock()
	defer directMu.Unlock()
	if works {
		delete(directBlocked, host)
	} else {
		directBlocked[host] = time.Now().Add(directBlockedFor)
	}
}

type fetched struct {
	response *http.Response
	body     []byte
	err      error
	direct   bool
}

// get выполняет запрос и сразу дочитывает ответ (не больше limit байт).
//
// При включённом VPN подписка через туннель качалась 3–12 с (проверено
// 15.11: TLS к серверу подписки 4–11 с), а напрямую — 0,35 с. Поэтому, когда
// известен прямой путь, сначала пробуем его, а через directHeadStart
// параллельно запускаем обычный; берётся первый удачный ответ.
func (f *Fetcher) get(ctx context.Context, build func(context.Context) (*http.Request, error), limit int64) (*http.Response, []byte, error) {
	var dial DialFunc
	if f.DirectDialer != nil {
		dial = f.DirectDialer(ctx)
	}
	probe, err := build(ctx)
	if err != nil {
		return nil, nil, err
	}
	host := probe.URL.Host
	if dial == nil || !directAllowed(host) {
		return f.once(ctx, f.Client, build, limit)
	}

	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan fetched, 2)
	run := func(client *http.Client, direct bool) {
		// Любой HTTP-ответ, даже 502, — окончательный: сервер достигнут.
		// Повтор через туннель нужен только при сетевой ошибке.
		response, body, err := f.once(raceCtx, client, build, limit)
		if direct && raceCtx.Err() == nil {
			markDirect(host, err == nil)
		}
		results <- fetched{response, body, err, direct}
	}
	go run(f.directClient(dial), true)

	pending, tunnelStarted, directDone := 1, false, false
	startTunnel := func() {
		if !tunnelStarted {
			tunnelStarted = true
			pending++
			go run(f.Client, false)
		}
	}
	timer := time.NewTimer(directHeadStart)
	defer timer.Stop()
	var lastErr error
	for {
		select {
		case <-timer.C:
			startTunnel()
		case result := <-results:
			pending--
			if result.direct {
				directDone = true
			}
			if result.err == nil {
				if !result.direct && !directDone {
					// Туннель успел раньше прямого, хотя стартовал позже, —
					// прямой путь к этому хосту закрыт или медленный.
					markDirect(host, false)
				}
				return result.response, result.body, nil
			}
			// Ответ через туннель важнее для текста ошибки: прямой путь —
			// лишь ускорение.
			if lastErr == nil || tunnelStarted {
				lastErr = result.err
			}
			startTunnel() // прямой путь не сработал — сразу через туннель
			if pending == 0 {
				return nil, nil, lastErr
			}
		case <-ctx.Done():
			if lastErr != nil {
				return nil, nil, lastErr
			}
			return nil, nil, ctx.Err()
		}
	}
}

func (f *Fetcher) once(ctx context.Context, client *http.Client, build func(context.Context) (*http.Request, error), limit int64) (*http.Response, []byte, error) {
	request, err := build(ctx)
	if err != nil {
		return nil, nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit))
	if err != nil {
		return nil, nil, fmt.Errorf("чтение ответа: %w", err)
	}
	return response, body, nil
}

func (f *Fetcher) directClient(dial DialFunc) *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		DisableKeepAlives:     true,
	}
	timeout := 35 * time.Second
	var redirect func(*http.Request, []*http.Request) error
	if f.Client != nil {
		redirect = f.Client.CheckRedirect
		if f.Client.Timeout > 0 {
			timeout = f.Client.Timeout
		}
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: redirect}
}
