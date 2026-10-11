package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ms7vpn/internal/model"
)

// Чужое значение режима должно молча становиться обычным VPN.
//
// Поле приходит из интерфейса, то есть от всякого, кто добрался до локального
// токена. Неизвестный режим без проверки означал бы состояние, в котором
// кнопка «Включить» не делает ничего.
func TestNormalizeSettingsMode(t *testing.T) {
	for _, value := range []string{model.ModeVPN, model.ModeZapret} {
		settings := model.DefaultSettings()
		settings.Mode = value
		if got := normalizeSettings(settings).Mode; got != value {
			t.Errorf("режим %q заменён на %q", value, got)
		}
	}
	for _, value := range []string{"", "ЗАПРЕТ", "tun", "zapret2", "../"} {
		settings := model.DefaultSettings()
		settings.Mode = value
		if got := normalizeSettings(settings).Mode; got != model.ModeVPN {
			t.Errorf("негодный режим %q остался как %q", value, got)
		}
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("приложение не создалось: %v", err)
	}
	t.Cleanup(application.Shutdown)
	return application
}

// В режиме «Запрет» кнопка «Включить» не должна уходить в путь Xray: сервера
// в этом режиме нет, и «Сначала выберите сервер» было бы неправдой.
func TestConnectInZapretModeGoesToZapret(t *testing.T) {
	application := newTestApp(t)
	application.mu.Lock()
	application.state.Settings.Mode = model.ModeZapret
	application.mu.Unlock()

	_, err := application.Connect(context.Background(), "", "")
	if err == nil {
		t.Skip("файлы режима найдены рядом с тестом и права есть — включение прошло")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("непонятная ошибка: %v", err)
	}
	switch apiErr.Code {
	case "ZAPRET_UNAVAILABLE", "NEED_ADMIN", "ZAPRET_START_FAILED", "ZAPRET_NO_STRATEGY":
	default:
		t.Errorf("запрос ушёл не в «Запрет»: код %s (%s)", apiErr.Code, apiErr.Message)
	}
}

// Смена VPN ⇄ «Запрет» на включённом соединении разрешена: это разные
// способы сделать одно и то же, и человек переключает их, чтобы сравнить.
// А смена способа туннелирования при живом подключении — нет.
func TestSettingsModeSwitchAllowedWhileConnected(t *testing.T) {
	application := newTestApp(t)
	application.mu.Lock()
	application.state.Connection.Connected = true
	application.mu.Unlock()

	settings := model.DefaultSettings()
	settings.Mode = model.ModeZapret
	if code := postSettings(t, application, settings); code != http.StatusOK {
		t.Errorf("смена режима отклонена с кодом %d", code)
	}

	application.mu.Lock()
	application.state.Settings.Mode = model.ModeVPN
	application.state.Connection.Connected = true
	application.mu.Unlock()

	tunnel := model.DefaultSettings()
	tunnel.ConnectionMode = "tun"
	if code := postSettings(t, application, tunnel); code != http.StatusConflict {
		t.Errorf("смена способа туннелирования на живом подключении прошла с кодом %d", code)
	}
}

func postSettings(t *testing.T, application *App, settings model.Settings) int {
	t.Helper()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(string(body)))
	recorder := httptest.NewRecorder()
	application.handleSettings(recorder, request)
	return recorder.Code
}

// Состояние режима отдаётся всегда, даже когда файлов нет: окно по нему
// решает, показывать кнопку включения или объяснение, почему нельзя.
func TestStateCarriesZapret(t *testing.T) {
	application := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	recorder := httptest.NewRecorder()
	application.handleState(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("состояние не отдано: код %d", recorder.Code)
	}
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			Zapret model.ZapretState `json:"zapret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ответ не разобран: %v", err)
	}
	if !payload.OK {
		t.Fatal("ответ без признака успеха")
	}
	if payload.Data.Zapret.Running {
		t.Error("режим считается включённым, хотя его не включали")
	}
}

// Подбор при автозапуске не трогаем, но включиться должны: главный экран
// понимает это по непустому значению.
func TestAutoConnectInZapretMode(t *testing.T) {
	application := newTestApp(t)
	application.mu.Lock()
	application.state.Settings.Mode = model.ModeZapret
	application.state.Settings.AutoConnect = true
	application.mu.Unlock()

	got := application.AutoConnectNodeID()
	if application.zapret.Available() {
		if got != model.ModeZapret {
			t.Errorf("при автозапуске «Запрета» получили %q", got)
		}
		return
	}
	if got != "" {
		t.Errorf("без файлов режима автозапуск вернул %q", got)
	}
}
