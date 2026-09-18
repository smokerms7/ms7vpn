package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ms7vpn/internal/model"
)

const maxSubscriptionBytes = 8 << 20

type Fetcher struct {
	Client   *http.Client
	DeviceID string
	DataDir  string
}

type FetchResult struct {
	Name                  string
	Nodes                 []model.Node
	Warnings              []string
	UserInfo              model.UserInfo
	UpdateIntervalMinutes int
	FinalURL              string
}

func NewFetcher(dataDir string) *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: 35 * time.Second,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("слишком много перенаправлений")
				}
				return nil
			},
		},
		DeviceID: loadOrCreateDeviceID(dataDir),
		DataDir:  dataDir,
	}
}

func loadOrCreateDeviceID(dataDir string) string {
	path := filepath.Join(dataDir, "device.id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if len(id) >= 10 && len(id) <= 64 {
			return id
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		// Extremely unlikely fallback; still stable after first successful write.
		return "MS7VPN-WINDOWS-DEVICE"
	}
	id := hex.EncodeToString(buf)
	_ = os.MkdirAll(dataDir, 0o700)
	_ = os.WriteFile(path, []byte(id), 0o600)
	return id
}

func (f *Fetcher) Fetch(ctx context.Context, source string) (FetchResult, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return FetchResult{}, fmt.Errorf("ссылка пустая")
	}
	if shareSchemeRE.MatchString(source) {
		nodes, warnings, err := ParseContent([]byte(source))
		return FetchResult{Name: "Локальный профиль", Nodes: nodes, Warnings: warnings, FinalURL: source}, err
	}

	parsedURL, err := url.Parse(source)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return FetchResult{}, fmt.Errorf("нужна subscription-ссылка http/https или прямая share-ссылка")
	}

	// Remnawave may choose a response template by User-Agent. Existing installations
	// often already have rules for Happ, therefore MS7VPN first identifies itself as
	// a Happ-compatible desktop client and falls back to stricter legacy Happ UAs only
	// when the server explicitly returns 403. The stable HWID remains identical across
	// retries, so this does not create multiple devices.
	userAgents := []string{
		"Happ/4.3.0 MS7VPN/0.4.0 Windows",
		"Happ/4.3.0",
		"Happ/4.2.1",
	}
	var response *http.Response
	for attempt, userAgent := range userAgents {
		request, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if reqErr != nil {
			return FetchResult{}, reqErr
		}
		request.Header.Set("User-Agent", userAgent)
		request.Header.Set("X-Hwid", f.DeviceID)
		request.Header.Set("X-Device-Os", "Windows")
		request.Header.Set("X-Ver-Os", "Windows 10/11")
		request.Header.Set("X-Device-Model", "MS7VPN Desktop")
		request.Header.Set("Accept", "text/plain, application/json, */*")
		request.Header.Set("Cache-Control", "no-cache")

		response, err = f.Client.Do(request)
		if err != nil {
			return FetchResult{}, fmt.Errorf("не удалось скачать подписку: %w", err)
		}
		if response.StatusCode != http.StatusForbidden || attempt == len(userAgents)-1 {
			break
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 2048))
		_ = response.Body.Close()
	}
	if response == nil {
		return FetchResult{}, fmt.Errorf("сервер подписки не ответил")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		message := strings.TrimSpace(string(body))
		if response.StatusCode == http.StatusForbidden {
			if message == "" {
				message = "доступ запрещён правилами подписки"
			}
			return FetchResult{}, fmt.Errorf("нет доступа к подписке (HTTP 403): %s", message)
		}
		return FetchResult{}, fmt.Errorf("сервер подписки ответил %s: %s", response.Status, message)
	}
	if strings.EqualFold(response.Header.Get("x-hwid-max-devices-reached"), "true") {
		return FetchResult{}, fmt.Errorf("достигнут лимит устройств этой подписки — сбросьте устройства в Telegram-боте и повторите")
	}
	if strings.EqualFold(response.Header.Get("x-hwid-not-supported"), "true") {
		return FetchResult{}, fmt.Errorf("сервер требует HWID-идентификацию устройства, но отклонил текущий идентификатор")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSubscriptionBytes+1))
	if err != nil {
		return FetchResult{}, fmt.Errorf("чтение подписки: %w", err)
	}
	if len(body) > maxSubscriptionBytes {
		return FetchResult{}, fmt.Errorf("подписка больше 8 МБ")
	}

	nodes, warnings, err := ParseContent(body)
	if err != nil {
		// Сохраняем ответ сервера, чтобы можно было понять формат подписки.
		f.saveRawResponse(body, response.Header.Get("Content-Type"))
		return FetchResult{}, fmt.Errorf("%w (сервер вернул %s, ответ сохранён в last-subscription.txt)",
			err, describeBody(body, response.Header.Get("Content-Type")))
	}

	name := ParseProfileTitle(firstHeader(response.Header, "profile-title", "x-profile-title", "subscription-title"))
	if name == "" {
		name = parsedURL.Hostname()
	}
	if name == "" {
		name = "MS7VPN"
	}

	// Заголовок обычно содержит только израсходованный трафик. Срок действия,
	// лимит и статус панель отдаёт отдельным адресом, поэтому спрашиваем и его.
	info := ParseSubscriptionUserInfo(firstHeader(response.Header, "subscription-userinfo"))
	infoCtx, cancelInfo := context.WithTimeout(ctx, 10*time.Second)
	info = f.FetchInfo(infoCtx, response.Request.URL.String(), info)
	cancelInfo()

	return FetchResult{
		Name:                  name,
		Nodes:                 nodes,
		Warnings:              warnings,
		UserInfo:              info,
		UpdateIntervalMinutes: parseUpdateInterval(firstHeader(response.Header, "profile-update-interval")),
		FinalURL:              response.Request.URL.String(),
	}, nil
}

// saveRawResponse кладёт непонятый ответ подписки в папку данных приложения.
func (f *Fetcher) saveRawResponse(body []byte, contentType string) {
	if f.DataDir == "" {
		return
	}
	limit := len(body)
	if limit > 512*1024 {
		limit = 512 * 1024
	}
	header := fmt.Sprintf("Content-Type: %s\nПолучено: %s\n\n", contentType, time.Now().Format(time.RFC3339))
	_ = os.WriteFile(filepath.Join(f.DataDir, "last-subscription.txt"), append([]byte(header), body[:limit]...), 0o600)
}

// describeBody коротко описывает, что именно пришло от сервера.
func describeBody(body []byte, contentType string) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 60 {
		text = text[:60]
	}
	text = strings.ReplaceAll(text, "\n", " ")
	kind := "текст"
	switch {
	case strings.HasPrefix(text, "<"):
		kind = "HTML-страницу"
	case strings.HasPrefix(text, "{") || strings.HasPrefix(text, "["):
		kind = "JSON"
	}
	if contentType != "" {
		return fmt.Sprintf("%s (%s): %s…", kind, contentType, text)
	}
	return fmt.Sprintf("%s: %s…", kind, text)
}

func firstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func parseUpdateInterval(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	// Happ-compatible profile-update-interval is conventionally expressed in hours.
	if hours, err := strconv.ParseFloat(value, 64); err == nil && hours > 0 {
		minutes := int(hours * 60)
		if minutes < 5 {
			minutes = 5
		}
		if minutes > 30*24*60 {
			minutes = 30 * 24 * 60
		}
		return minutes
	}
	if duration, err := time.ParseDuration(value); err == nil {
		minutes := int(duration.Minutes())
		if minutes < 5 {
			minutes = 5
		}
		return minutes
	}
	return 0
}
