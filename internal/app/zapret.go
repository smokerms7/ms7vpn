package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"ms7vpn/internal/model"
	"ms7vpn/internal/zapret"
)

// Режим «Запрет» живёт рядом с обычным подключением, но ничего общего с ним
// не имеет: сервера нет, портов нет, системный прокси не трогается. Общая у
// них только кнопка «Включить» — поэтому /api/connect и /api/disconnect
// расходятся по режиму здесь, а не в интерфейсе.

// pick — состояние автоподбора. Держится отдельно от a.state: подбор идёт
// минутами, а состояние окна опрашивается каждые 2,5 секунды.
type pickRun struct {
	mu     sync.Mutex
	state  model.PickState
	cancel context.CancelFunc
}

func (p *pickRun) snapshot() model.PickState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *pickRun) set(state model.PickState) {
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
}

// begin отмечает начало подбора. Возвращает false, если подбор уже идёт:
// две копии дрались бы за один драйвер.
func (p *pickRun) begin(cancel context.CancelFunc) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.Running {
		return false
	}
	p.state = model.PickState{Running: true}
	p.cancel = cancel
	return true
}

// stop прерывает подбор. Кнопка «Остановить» должна срабатывать мгновенно,
// поэтому отмена идёт через контекст, а не ожиданием конца текущего шага.
func (p *pickRun) stop() {
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) registerZapretRoutes(mux *http.ServeMux, withAuth func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/api/zapret/strategies", withAuth(a.handleZapretStrategies))
	mux.HandleFunc("/api/zapret/select", withAuth(a.handleZapretSelect))
	mux.HandleFunc("/api/zapret/pick", withAuth(a.handleZapretPick))
	mux.HandleFunc("/api/zapret/pick/stop", withAuth(a.handleZapretPickStop))
}

// zapretSnapshot собирает состояние режима для окна.
func (a *App) zapretSnapshot() model.ZapretState {
	state := model.ZapretState{
		Available: a.zapret.Available(),
		Running:   a.zapret.Running(),
		StartedAt: a.zapret.StartedAt(),
		Picked:    a.zapret.Prefs().Picked,
		LastError: a.zapret.LastExit(),
		Pick:      a.pick.snapshot(),
	}
	if current := a.zapret.CurrentStrategy(); current.ID != "" {
		state.StrategyID, state.StrategyName = current.ID, current.Name
	} else if selected, err := a.zapret.Selected(); err == nil {
		state.StrategyID, state.StrategyName = selected.ID, selected.Name
	}
	return state
}

// EnableZapret включает обход.
func (a *App) EnableZapret(parent context.Context) (model.AppState, error) {
	if !a.zapret.Available() {
		return a.Snapshot(), &APIError{Code: "ZAPRET_UNAVAILABLE",
			Message: "Файлы режима «Запрет» не найдены рядом с программой. Переустановите MS7VPN"}
	}
	// Драйвер перехвата без прав администратора не поднимается. Говорим об
	// этом до запуска: иначе человек увидел бы «отказано в доступе» от winws.
	if !zapret.IsAdministrator() {
		return a.Snapshot(), &APIError{Code: "NEED_ADMIN",
			Message: "Для режима «Запрет» нужны права администратора"}
	}
	if a.pick.snapshot().Running {
		return a.Snapshot(), &APIError{Code: "PICK_RUNNING",
			Message: "Идёт подбор стратегии — дождитесь конца или остановите его"}
	}

	// Туннель и обход вместе не работают: winws не знает про адаптер и
	// начнёт портить его собственные пакеты.
	a.mu.RLock()
	tunnelUp := a.state.Connection.Connected || a.state.Connection.Connecting
	tunnelMode := a.state.Connection.Mode
	a.mu.RUnlock()
	if tunnelUp && tunnelMode != model.ModeZapret {
		if _, err := a.Disconnect(); err != nil {
			return a.Snapshot(), err
		}
	}

	strategy, err := a.zapret.Selected()
	if err != nil {
		return a.Snapshot(), &APIError{Code: "ZAPRET_NO_STRATEGY", Message: err.Error()}
	}

	a.mu.Lock()
	a.state.Connection = model.ConnectionState{
		Connecting: true, Mode: model.ModeZapret,
		NodeName: strategy.Name, StatusMessage: "Поднимаем драйвер перехвата…",
	}
	a.mu.Unlock()

	a.logf("Запрет: включение, стратегия %q (%s)", strategy.Name, strategy.ID)
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	startErr := a.zapret.Start(ctx, strategy)
	cancel()
	if startErr != nil {
		a.logf("Запрет: ОШИБКА — %v", startErr)
		a.mu.Lock()
		a.state.Connection = model.ConnectionState{
			Mode: model.ModeZapret, LastError: startErr.Error(), StatusMessage: "Не включилось",
		}
		state := a.state
		a.mu.Unlock()
		return state, &APIError{Code: "ZAPRET_START_FAILED", Message: startErr.Error()}
	}

	a.mu.Lock()
	a.state.Connection = model.ConnectionState{
		Connected: true, Mode: model.ModeZapret, NodeName: strategy.Name,
		StartedAt: time.Now(), StatusMessage: "Включено",
	}
	state := a.state
	_ = a.store.Save(a.state)
	a.mu.Unlock()
	a.logf("Запрет: включено (%s)", strategy.ID)
	return state, nil
}

// DisableZapret выключает обход.
func (a *App) DisableZapret() (model.AppState, error) {
	a.logf("Запрет: выключение")
	a.pick.stop()
	stopErr := a.zapret.Stop()
	a.mu.Lock()
	a.state.Connection = model.ConnectionState{Mode: model.ModeZapret, StatusMessage: "Выключено"}
	state := a.state
	a.mu.Unlock()
	if stopErr != nil {
		return state, &APIError{Code: "ZAPRET_STOP_FAILED", Message: stopErr.Error()}
	}
	return state, nil
}

func (a *App) handleZapretStrategies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	catalog, err := a.zapret.Catalog()
	if err != nil {
		writeError(w, http.StatusBadRequest, &APIError{Code: "ZAPRET_NO_STRATEGY", Message: err.Error()})
		return
	}
	writeOK(w, catalog)
}

type zapretSelectRequest struct {
	ID string `json:"id"`
}

// handleZapretSelect сохраняет выбранную стратегию.
//
// Если обход включён, он переподнимается с новой стратегией сразу: человек
// выбирает строку в списке и ждёт результата, а не ещё одного нажатия.
func (a *App) handleZapretSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request zapretSelectRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.zapret.Select(request.ID); err != nil {
		writeError(w, http.StatusBadRequest, &APIError{Code: "ZAPRET_NO_STRATEGY", Message: err.Error()})
		return
	}
	a.logf("Запрет: выбрана стратегия %s", request.ID)
	if a.zapret.Running() {
		if _, err := a.DisableZapret(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		state, err := a.EnableZapret(r.Context())
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeOK(w, state)
		return
	}
	writeOK(w, a.Snapshot())
}

// handleZapretPick запускает автоподбор.
//
// Подбор идёт в своей горутине и минутами: двадцать две стратегии, на каждую
// запуск winws, проверка YouTube и Discord, остановка. Ход подбора окно
// забирает вместе с состоянием.
func (a *App) handleZapretPick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	if !a.zapret.Available() {
		writeError(w, http.StatusBadRequest, &APIError{Code: "ZAPRET_UNAVAILABLE",
			Message: "Файлы режима «Запрет» не найдены рядом с программой"})
		return
	}
	if !zapret.IsAdministrator() {
		writeError(w, http.StatusConflict, &APIError{Code: "NEED_ADMIN",
			Message: "Для подбора нужны права администратора"})
		return
	}
	// Подбор проверяет сайты напрямую. С поднятым туннелем проверка
	// показывала бы, что всё открылось, при любой стратегии.
	a.mu.RLock()
	tunnelUp := (a.state.Connection.Connected || a.state.Connection.Connecting) &&
		a.state.Connection.Mode != model.ModeZapret
	a.mu.RUnlock()
	if tunnelUp {
		writeError(w, http.StatusConflict, &APIError{Code: "DISCONNECT_FIRST",
			Message: "Сначала отключите VPN: подбор проверяет сайты напрямую"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	if !a.pick.begin(cancel) {
		cancel()
		writeError(w, http.StatusConflict, &APIError{Code: "PICK_RUNNING", Message: "Подбор уже идёт"})
		return
	}

	go a.runPick(ctx, cancel)
	writeOK(w, a.zapretSnapshot())
}

// runPick выполняет подбор и приводит состояние в порядок после него.
func (a *App) runPick(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()

	wasRunning := a.zapret.Running()
	if wasRunning {
		// Подбор поднимает обход сам, с каждой стратегией по очереди.
		_, _ = a.DisableZapret()
	}
	a.logf("Запрет: начат подбор стратегии")

	strategy, probe, err := a.zapret.AutoPick(ctx, func(progress zapret.Progress) {
		if progress.StepError != "" {
			a.logf("Запрет: стратегия не запустилась — %s", progress.StepError)
		}
		a.pick.set(model.PickState{
			Running: progress.Running, Index: progress.Index, Total: progress.Total,
			Current: progress.Current, CurrentID: progress.CurrentID,
			Done: progress.Done, Found: progress.Found, FoundID: progress.FoundID,
			Full: progress.Full, Error: progress.Error,
		})
	})

	switch {
	case errors.Is(err, context.Canceled):
		a.logf("Запрет: подбор остановлен")
		a.pick.set(model.PickState{Done: true, Error: "Подбор остановлен"})
	case err != nil:
		a.logf("Запрет: подбор без результата — %v", err)
		a.pick.set(model.PickState{Done: true, Error: err.Error()})
	default:
		_ = a.zapret.Select(strategy.ID)
		a.logf("Запрет: подобрана стратегия %q (YouTube %v, Discord %v)",
			strategy.Name, probe.YouTube, probe.Discord)
		a.pick.set(model.PickState{Done: true, Found: strategy.Name, FoundID: strategy.ID,
			Full: probe.YouTube && probe.Discord})
	}

	// После подбора обход выключен: AutoPick останавливает winws на каждом
	// шаге. Если он был включён до подбора — возвращаем как было.
	if wasRunning && err == nil {
		if _, enableErr := a.EnableZapret(context.Background()); enableErr != nil {
			a.logf("Запрет: не удалось включить после подбора — %v", enableErr)
		}
	}
}

func (a *App) handleZapretPickStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	a.pick.stop()
	writeOK(w, a.zapretSnapshot())
}
