//go:build windows

package webui

import (
	"context"
	"fmt"
	"os"
	"time"

	ms7app "ms7vpn/internal/app"
	"ms7vpn/internal/model"
	"ms7vpn/internal/singleton"
	"ms7vpn/internal/tray"
)

// startTray создаёт значок в области уведомлений и следит за состоянием
// приложения. Возвращает функцию остановки.
//
// Если Windows по какой-то причине не дала создать значок, приложение просто
// работает без него: значок — удобство, а не обязательное условие работы.
func startTray(app *ms7app.App, windowHandle uintptr) func() {
	stop := make(chan struct{})
	tray.SetShowMessage(singleton.ShowMessage())

	icon, err := tray.New(tray.Handlers{
		OnShowWindow: tray.ShowMainWindow,
		OnToggleConnect: func() {
			state := app.Snapshot()
			if state.Connection.Connecting {
				return
			}
			if state.Connection.Connected {
				_, _ = app.Disconnect()
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, _ = app.Connect(ctx, "", "")
		},
		OnRefreshSubs: func() {
			for _, sub := range app.Snapshot().Subscriptions {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				_, _ = app.RefreshSubscription(ctx, sub.ID)
				cancel()
			}
		},
		OnQuit: app.Shutdown,
	})
	if err != nil {
		return func() {}
	}

	// Крестик прячет окно в трей, если это разрешено настройками. Выход —
	// через меню значка или кнопку в окне, и тогда Xray корректно
	// останавливается, а системный прокси возвращается на место.
	tray.AttachWindow(windowHandle, func() bool {
		return app.Snapshot().Settings.MinimizeToTray
	})

	if executable, execErr := os.Executable(); execErr == nil {
		tray.PromoteIcon(executable)
	}

	go icon.Run()
	go watchState(app, icon, stop)

	return func() {
		close(stop)
		icon.Close()
	}
}

// watchState обновляет значок и показывает уведомления о смене состояния.
func watchState(app *ms7app.App, icon *tray.Tray, stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var previous model.ConnectionState
	first := true
	for {
		select {
		case <-stop:
			return
		case <-app.Done():
			return
		case <-ticker.C:
		}

		snapshot := app.Snapshot()
		current := snapshot.Connection
		icon.SetState(trayState(current), trayTooltip(current))

		if !first {
			switch {
			case current.Connected && !previous.Connected:
				icon.Notify("MS7VPN подключён", connectionSummary(current))
			case !current.Connected && previous.Connected:
				icon.Notify("MS7VPN отключён", "Трафик снова идёт напрямую")
			case current.LastError != "" && current.LastError != previous.LastError:
				icon.Notify("Не удалось подключиться", current.LastError)
			}
		}
		previous = current
		first = false
	}
}

func trayState(connection model.ConnectionState) tray.State {
	switch {
	case connection.Connected:
		return tray.StateOn
	case connection.Connecting:
		return tray.StateConnecting
	default:
		return tray.StateOff
	}
}

func trayTooltip(connection model.ConnectionState) string {
	switch {
	case connection.Connected:
		return fmt.Sprintf("MS7VPN — %s\n%s", connection.NodeName, uptimeText(connection.StartedAt))
	case connection.Connecting:
		return "MS7VPN — подключение…"
	default:
		return "MS7VPN — отключено"
	}
}

func connectionSummary(connection model.ConnectionState) string {
	mode := "системный прокси"
	if connection.Mode == "tun" {
		mode = "TUN, весь трафик"
	}
	return fmt.Sprintf("%s · %s", connection.NodeName, mode)
}

func uptimeText(startedAt time.Time) string {
	if startedAt.IsZero() {
		return ""
	}
	elapsed := time.Since(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	total := int(elapsed.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}
