package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ms7vpn/internal/model"
	"ms7vpn/internal/store"
	"ms7vpn/internal/subscription"
	"ms7vpn/internal/update"
	"ms7vpn/internal/xray"
)

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Message }

type App struct {
	mu      sync.RWMutex
	state   model.AppState
	store   *store.Store
	fetcher *subscription.Fetcher
	core    *xray.Manager
	dataDir string
	done    chan struct{}
	quit    sync.Once

	// Порты подбираются при подключении: 10808/10809 могут быть заняты
	// соседним v2rayN или Nekoray.
	portMu    sync.Mutex
	httpPort  int
	socksPort int

	// Счётчики трафика: клиент локального API Xray.
	statsMu     sync.Mutex
	statsClient *xray.StatsClient

	// Отмена незавершённого подключения. Без неё нажатие «Отключить» во
	// время долгого подключения не останавливало перебор адаптеров: Xray
	// запускался снова и снова уже после отключения.
	cancelMu      sync.Mutex
	cancelConnect context.CancelFunc
}

// beginConnect регистрирует текущую попытку подключения и отменяет предыдущую.
func (a *App) beginConnect(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	a.cancelMu.Lock()
	if a.cancelConnect != nil {
		a.cancelConnect()
	}
	a.cancelConnect = cancel
	a.cancelMu.Unlock()
	return ctx, func() {
		cancel()
		a.cancelMu.Lock()
		if a.cancelConnect != nil {
			a.cancelConnect = nil
		}
		a.cancelMu.Unlock()
	}
}

// abortConnect прерывает подключение, которое ещё выполняется.
func (a *App) abortConnect() {
	a.cancelMu.Lock()
	cancel := a.cancelConnect
	a.cancelConnect = nil
	a.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func New(dataDir string) (*App, error) {
	if dataDir == "" {
		return nil, errors.New("dataDir is empty")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	st := store.New(dataDir)
	state, err := st.Load()
	if err != nil {
		// Recover with defaults but preserve a clear visible error.
		state = model.DefaultState()
		state.Connection.LastError = err.Error()
	}
	state.Version = model.AppVersion
	application := &App{
		state: state, store: st, fetcher: subscription.NewFetcher(dataDir),
		dataDir: dataDir, done: make(chan struct{}),
	}
	application.core = xray.NewManager(dataDir, application.onCoreProgress)
	application.state.Core.Version = xray.CoreVersion
	application.state.Core.Installed = application.core.Installed()

	// Хвосты предыдущего сеанса: если приложение было убито, xray.exe мог
	// остаться жив и держать порты и сетевой адаптер, а системный прокси —
	// остаться включённым, из-за чего интернет не работал вообще.
	xray.KillOrphans(application.core.CorePath())
	_ = RecoverSystemProxy(dataDir)
	return application, nil
}

func (a *App) Done() <-chan struct{} { return a.done }
func (a *App) DataDir() string       { return a.dataDir }

const maxAppLogBytes = 256 << 10

// logf пишет в собственный журнал приложения.
//
// Раньше приложение не вело журнала вообще: при неудачном подключении
// оставался только лог Xray, по которому невозможно понять, на каком шаге
// всё сорвалось и с какой ошибкой.
func (a *App) logf(format string, args ...any) {
	path := filepath.Join(a.dataDir, "ms7vpn.log")
	if info, err := os.Stat(path); err == nil && info.Size() > maxAppLogBytes {
		_ = os.Remove(path)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s  %s\n",
		time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

// AppLog возвращает последние строки журнала приложения.
func (a *App) AppLog(lines int) string {
	data, err := os.ReadFile(filepath.Join(a.dataDir, "ms7vpn.log"))
	if err != nil {
		return ""
	}
	return tailLines(string(data), lines)
}

func (a *App) Snapshot() model.AppState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	copyState := a.state
	copyState.Subscriptions = append([]model.Subscription(nil), a.state.Subscriptions...)
	return copyState
}

func (a *App) onCoreProgress(progress xray.Progress) {
	a.mu.Lock()
	a.state.Core.Downloading = progress.Downloading
	a.state.Core.Progress = progress.Percent
	a.state.Core.StatusMessage = progress.Message
	a.state.Core.Installed = a.core != nil && a.core.Installed()
	if progress.Err != nil {
		a.state.Core.LastError = progress.Err.Error()
	} else if progress.Percent == 100 {
		a.state.Core.LastError = ""
	}
	a.mu.Unlock()
}

func (a *App) RegisterRoutes(mux *http.ServeMux, token string) {
	withAuth := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-MS7-Token") != token && r.URL.Query().Get("token") != token {
				writeError(w, http.StatusForbidden, &APIError{Code: "FORBIDDEN", Message: "Локальный токен приложения недействителен"})
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			handler(w, r)
		}
	}

	mux.HandleFunc("/api/state", withAuth(a.handleState))
	mux.HandleFunc("/api/subscriptions/add", withAuth(a.handleAddSubscription))
	mux.HandleFunc("/api/subscriptions/refresh", withAuth(a.handleRefreshSubscription))
	mux.HandleFunc("/api/subscriptions/delete", withAuth(a.handleDeleteSubscription))
	mux.HandleFunc("/api/nodes/select", withAuth(a.handleSelectNode))
	mux.HandleFunc("/api/nodes/favorite", withAuth(a.handleFavoriteNode))
	mux.HandleFunc("/api/nodes/ping", withAuth(a.handlePingNodes))
	mux.HandleFunc("/api/connect", withAuth(a.handleConnect))
	mux.HandleFunc("/api/disconnect", withAuth(a.handleDisconnect))
	mux.HandleFunc("/api/settings", withAuth(a.handleSettings))
	mux.HandleFunc("/api/log", withAuth(a.handleLog))
	mux.HandleFunc("/api/open", withAuth(a.handleOpenURL))
	mux.HandleFunc("/api/open-data-folder", withAuth(a.handleOpenDataFolder))
	mux.HandleFunc("/api/elevate", withAuth(a.handleElevate))
	mux.HandleFunc("/api/update/check", withAuth(a.handleCheckUpdate))
	mux.HandleFunc("/api/quit", withAuth(a.handleQuit))
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	// Опрос ядра выполняется ДО захвата a.mu. Раньше handleState держал a.mu
	// и внутри лез в мьютекс менеджера, который был занят стартом Xray, —
	// из-за этого весь интерфейс замирал на время подключения.
	installed := a.core.Installed()
	running := a.core.Running()

	a.mu.Lock()
	a.state.Core.Installed = installed
	dropped := a.state.Connection.Connected && !running
	if dropped {
		a.state.Connection.Connected = false
		a.state.Connection.Connecting = false
		a.state.Connection.LastError = "Xray завершил работу. Откройте журнал для причины."
	}
	state := a.state
	a.mu.Unlock()
	if dropped {
		_ = RestoreSystemProxy(a.dataDir)
	}
	writeOK(w, state)
}

type addSubscriptionRequest struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

func (a *App) handleAddSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request addSubscriptionRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result, err := a.fetcher.Fetch(ctx, request.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, &APIError{Code: "SUBSCRIPTION_IMPORT_FAILED", Message: err.Error()})
		return
	}
	if len(result.Nodes) > 5000 {
		writeError(w, http.StatusBadRequest, &APIError{Code: "TOO_MANY_NODES", Message: "В одной подписке больше 5000 серверов"})
		return
	}

	now := time.Now()
	id := subscription.StableID(strings.TrimSpace(request.URL))
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = result.Name
	}
	newSubscription := model.Subscription{
		ID: id, Name: name, URL: strings.TrimSpace(request.URL), Nodes: result.Nodes,
		UpdatedAt: now, CreatedAt: now, UpdateIntervalMinutes: result.UpdateIntervalMinutes,
		UserInfo: result.UserInfo, Warnings: result.Warnings,
	}

	a.mu.Lock()
	replaced := false
	for index := range a.state.Subscriptions {
		if a.state.Subscriptions[index].ID == id || a.state.Subscriptions[index].URL == newSubscription.URL {
			newSubscription.CreatedAt = a.state.Subscriptions[index].CreatedAt
			preserveNodeRuntime(&newSubscription, a.state.Subscriptions[index])
			a.state.Subscriptions[index] = newSubscription
			replaced = true
			break
		}
	}
	if !replaced {
		if len(a.state.Subscriptions) >= 500 {
			a.mu.Unlock()
			writeError(w, http.StatusBadRequest, &APIError{Code: "TOO_MANY_SUBSCRIPTIONS", Message: "Лимит alpha-сборки: 500 подписок"})
			return
		}
		a.state.Subscriptions = append(a.state.Subscriptions, newSubscription)
	}
	a.state.SelectedSubID = id
	if a.state.SelectedNodeID == "" || !a.nodeExistsLocked(a.state.SelectedNodeID) {
		a.state.SelectedNodeID = firstSupportedNodeID(newSubscription.Nodes)
	}
	state := a.state
	saveErr := a.store.Save(a.state)
	a.mu.Unlock()
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, &APIError{Code: "SAVE_FAILED", Message: saveErr.Error()})
		return
	}
	writeOK(w, state)
}

type subscriptionIDRequest struct {
	ID string `json:"id"`
}

func (a *App) handleRefreshSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request subscriptionIDRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	a.mu.RLock()
	var current model.Subscription
	found := false
	for _, item := range a.state.Subscriptions {
		if item.ID == request.ID {
			current, found = item, true
			break
		}
	}
	a.mu.RUnlock()
	if !found {
		writeError(w, http.StatusNotFound, &APIError{Code: "SUBSCRIPTION_NOT_FOUND", Message: "Подписка не найдена"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result, err := a.fetcher.Fetch(ctx, current.URL)
	if err != nil {
		a.mu.Lock()
		for index := range a.state.Subscriptions {
			if a.state.Subscriptions[index].ID == current.ID {
				a.state.Subscriptions[index].LastError = err.Error()
			}
		}
		_ = a.store.Save(a.state)
		a.mu.Unlock()
		writeError(w, http.StatusBadRequest, &APIError{Code: "SUBSCRIPTION_REFRESH_FAILED", Message: err.Error()})
		return
	}

	updated := current
	updated.Nodes = result.Nodes
	updated.UpdatedAt = time.Now()
	updated.LastError = ""
	updated.Warnings = result.Warnings
	updated.UserInfo = result.UserInfo
	if result.Name != "" {
		updated.Name = result.Name
	}
	if result.UpdateIntervalMinutes > 0 {
		updated.UpdateIntervalMinutes = result.UpdateIntervalMinutes
	}
	preserveNodeRuntime(&updated, current)

	a.mu.Lock()
	for index := range a.state.Subscriptions {
		if a.state.Subscriptions[index].ID == current.ID {
			a.state.Subscriptions[index] = updated
			break
		}
	}
	if !a.nodeExistsLocked(a.state.SelectedNodeID) {
		a.state.SelectedNodeID = firstSupportedNodeID(updated.Nodes)
	}
	state := a.state
	saveErr := a.store.Save(a.state)
	a.mu.Unlock()
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, &APIError{Code: "SAVE_FAILED", Message: saveErr.Error()})
		return
	}
	writeOK(w, state)
}

func (a *App) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request subscriptionIDRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	a.mu.Lock()
	index := -1
	for i, item := range a.state.Subscriptions {
		if item.ID == request.ID {
			index = i
			break
		}
	}
	if index < 0 {
		a.mu.Unlock()
		writeError(w, http.StatusNotFound, &APIError{Code: "SUBSCRIPTION_NOT_FOUND", Message: "Подписка не найдена"})
		return
	}
	deleted := a.state.Subscriptions[index]
	if nodeInSubscription(deleted, a.state.Connection.NodeID) && (a.state.Connection.Connected || a.state.Connection.Connecting) {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, &APIError{Code: "SUBSCRIPTION_IN_USE", Message: "Сначала отключите VPN"})
		return
	}
	for _, node := range deleted.Nodes {
		delete(a.state.Favorites, node.ID)
	}
	a.state.Subscriptions = append(a.state.Subscriptions[:index], a.state.Subscriptions[index+1:]...)
	if a.state.SelectedSubID == request.ID {
		a.state.SelectedSubID = ""
		if len(a.state.Subscriptions) > 0 {
			a.state.SelectedSubID = a.state.Subscriptions[0].ID
		}
	}
	if !a.nodeExistsLocked(a.state.SelectedNodeID) {
		a.state.SelectedNodeID = a.firstSupportedNodeLocked()
	}
	state := a.state
	saveErr := a.store.Save(a.state)
	a.mu.Unlock()
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, &APIError{Code: "SAVE_FAILED", Message: saveErr.Error()})
		return
	}
	writeOK(w, state)
}

type nodeIDRequest struct {
	NodeID string `json:"nodeId"`
}

func (a *App) handleSelectNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request nodeIDRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.mu.Lock()
	node, subID, found := a.findNodeLocked(request.NodeID)
	if !found {
		a.mu.Unlock()
		writeError(w, http.StatusNotFound, &APIError{Code: "NODE_NOT_FOUND", Message: "Сервер не найден"})
		return
	}
	if !node.Supported {
		a.mu.Unlock()
		writeError(w, http.StatusBadRequest, &APIError{Code: "NODE_UNSUPPORTED", Message: node.UnsupportedReason})
		return
	}
	a.state.SelectedNodeID = node.ID
	a.state.SelectedSubID = subID
	_ = a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	writeOK(w, state)
}

func (a *App) handleFavoriteNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request nodeIDRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.mu.Lock()
	if !a.nodeExistsLocked(request.NodeID) {
		a.mu.Unlock()
		writeError(w, http.StatusNotFound, &APIError{Code: "NODE_NOT_FOUND", Message: "Сервер не найден"})
		return
	}
	a.state.Favorites[request.NodeID] = !a.state.Favorites[request.NodeID]
	value := a.state.Favorites[request.NodeID]
	_ = a.store.Save(a.state)
	a.mu.Unlock()
	writeOK(w, map[string]any{"nodeId": request.NodeID, "favorite": value})
}

type pingRequest struct {
	SubscriptionID string   `json:"subscriptionId"`
	NodeIDs        []string `json:"nodeIds"`
}

func (a *App) handlePingNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request pingRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	a.mu.RLock()
	var targets []model.Node
	wanted := make(map[string]bool)
	for _, id := range request.NodeIDs {
		wanted[id] = true
	}
	for _, item := range a.state.Subscriptions {
		if request.SubscriptionID != "" && item.ID != request.SubscriptionID {
			continue
		}
		for _, node := range item.Nodes {
			if len(wanted) == 0 || wanted[node.ID] {
				targets = append(targets, node)
			}
		}
	}
	a.mu.RUnlock()
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, &APIError{Code: "NO_NODES", Message: "Нет серверов для проверки"})
		return
	}
	if len(targets) > 500 {
		targets = targets[:500]
	}

	type result struct {
		ID     string
		PingMS int
		Status string
	}
	jobs := make(chan model.Node)
	results := make(chan result, len(targets))
	workerCount := 12
	if len(targets) < workerCount {
		workerCount = len(targets)
	}
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for node := range jobs {
				ctx, cancel := context.WithTimeout(r.Context(), 3500*time.Millisecond)
				duration, err := xray.PingTCP(ctx, node.Address, node.Port, 3*time.Second)
				cancel()
				if err != nil {
					results <- result{ID: node.ID, PingMS: -1, Status: "error"}
				} else {
					milliseconds := int(duration.Round(time.Millisecond) / time.Millisecond)
					if milliseconds < 1 {
						milliseconds = 1
					}
					results <- result{ID: node.ID, PingMS: milliseconds, Status: "ok"}
				}
			}
		}()
	}
	go func() {
		for _, node := range targets {
			jobs <- node
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	resultMap := make(map[string]result, len(targets))
	for item := range results {
		resultMap[item.ID] = item
	}
	now := time.Now()
	a.mu.Lock()
	for subIndex := range a.state.Subscriptions {
		for nodeIndex := range a.state.Subscriptions[subIndex].Nodes {
			node := &a.state.Subscriptions[subIndex].Nodes[nodeIndex]
			if item, ok := resultMap[node.ID]; ok {
				node.PingMS = item.PingMS
				node.PingStatus = item.Status
				node.LastPingAt = now
			}
		}
	}
	_ = a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	writeOK(w, state)
}

type connectRequest struct {
	NodeID string `json:"nodeId"`
	Mode   string `json:"mode"`
}

func (a *App) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request connectRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	state, err := a.Connect(r.Context(), request.NodeID, request.Mode)
	if err != nil {
		status := http.StatusBadRequest
		if apiErr, ok := err.(*APIError); ok && apiErr.Code == "NEED_ADMIN" {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	writeOK(w, state)
}

func (a *App) Connect(parent context.Context, nodeID, mode string) (model.AppState, error) {
	ctx, finish := a.beginConnect(parent)
	defer finish()

	a.mu.Lock()
	if a.state.Connection.Connecting {
		a.mu.Unlock()
		return model.AppState{}, &APIError{Code: "BUSY", Message: "Подключение уже выполняется"}
	}
	if nodeID == "" {
		nodeID = a.state.SelectedNodeID
	}
	node, subID, found := a.findNodeLocked(nodeID)
	if !found {
		a.mu.Unlock()
		return model.AppState{}, &APIError{Code: "NODE_NOT_FOUND", Message: "Сначала выберите сервер"}
	}
	if !node.Supported {
		a.mu.Unlock()
		return model.AppState{}, &APIError{Code: "NODE_UNSUPPORTED", Message: node.UnsupportedReason}
	}
	if mode == "" {
		mode = a.state.Settings.ConnectionMode
	}
	if mode != "tun" && mode != "system-proxy" {
		mode = "system-proxy"
	}
	var fallbackNote string
	if mode == "tun" && !IsAdministrator() {
		// Раньше здесь был отказ, и пользователь оставался без подключения
		// вовсе. Системный прокси прав не требует и работает всегда.
		mode = "system-proxy"
		fallbackNote = "TUN требует запуска от имени администратора — подключено через системный прокси"
	}
	a.state.Connection = model.ConnectionState{Connecting: true, NodeID: node.ID, NodeName: node.Name, Mode: mode, StatusMessage: "Подготовка Xray-core…"}
	a.state.SelectedNodeID = node.ID
	a.state.SelectedSubID = subID
	a.mu.Unlock()

	a.logf("подключение: сервер %q, режим %s", node.Name, mode)

	fail := func(err error) (model.AppState, error) {
		a.logf("ОШИБКА подключения: %v", err)
		_ = a.core.Stop()
		_ = RestoreSystemProxy(a.dataDir)
		a.mu.Lock()
		a.state.Connection.Connecting = false
		a.state.Connection.Connected = false
		a.state.Connection.LastError = err.Error()
		a.state.Connection.StatusMessage = "Ошибка подключения"
		state := a.state
		a.mu.Unlock()
		return state, &APIError{Code: "CONNECT_FAILED", Message: err.Error()}
	}

	// Настройки снимаются один раз. Раньше Snapshot() вызывался шесть раз
	// подряд внутри цикла, каждый раз копируя весь список серверов.
	settings := a.Snapshot().Settings

	ensureCtx, cancel := context.WithTimeout(ctx, 9*time.Minute)
	err := a.core.Ensure(ensureCtx)
	cancel()
	if err != nil {
		return fail(err)
	}

	if err := a.core.Stop(); err != nil {
		return fail(err)
	}
	// Оставшийся от прошлой попытки xray.exe держит сетевой адаптер и
	// локальные порты. Раньше это проверялось только при запуске программы,
	// поэтому повторное подключение билось в занятый порт снова и снова.
	if killed := xray.KillOrphans(a.core.CorePath()); killed > 0 {
		a.logf("закрыто зависших процессов Xray: %d", killed)
		time.Sleep(400 * time.Millisecond)
	}
	_ = RestoreSystemProxy(a.dataDir)

	httpPort, socksPort := 10809, 10808
	if mode != "tun" {
		httpPort = xray.FreeLocalPort(10809)
		socksPort = xray.FreeLocalPort(10808)
		if socksPort == httpPort {
			socksPort = xray.FreeLocalPort(httpPort + 1)
		}
	}
	a.portMu.Lock()
	a.httpPort, a.socksPort = httpPort, socksPort
	a.portMu.Unlock()
	if mode != "tun" {
		a.logf("порты: http=%d socks=%d", httpPort, socksPort)
	}

	// Порт счётчиков подбирается свободным. Зашитый 10085 в прошлой версии
	// ронял подключение целиком, когда его занимал прошлый процесс Xray.
	apiPort := xray.FreeLocalPort(xray.APIPort)
	a.statsMu.Lock()
	a.statsClient = xray.NewStatsClient(apiPort)
	a.statsMu.Unlock()

	// Имя сетевого адаптера для TUN. Если предыдущий запуск оборвался или другой
	// VPN-клиент держит адаптер, Wintun отвечает «файл уже существует», поэтому
	// пробуем несколько имён подряд.
	tunNames := []string{"MS7VPN"}
	if mode == "tun" {
		tunNames = append(tunNames, fmt.Sprintf("MS7VPN-%d", time.Now().Unix()%1000))
	}

	var warnings []string
	for attempt, tunName := range tunNames {
		built, buildErr := xray.Build(node, xray.ConfigOptions{
			Mode: mode, HTTPPort: httpPort, SOCKSPort: socksPort,
			DNS1: settings.DNS1, DNS2: settings.DNS2,
			BlockAds:    settings.BlockAds,
			AccessLog:   filepath.Join(a.core.RuntimeDir(), "access.log"),
			ErrorLog:    filepath.Join(a.core.RuntimeDir(), "error.log"),
			TunName:     tunName,
			GeoDir:      a.core.CoreDir(),
			APIPort:     apiPort,
			EnableStats: true,
		})
		if buildErr != nil {
			return fail(buildErr)
		}
		warnings = built.Warnings

		if startErr := a.core.Start(built.Config, mode); startErr != nil {
			return fail(startErr)
		}
		// Короткая проверка «процесс не умер сразу». Если умер — менеджер сам
		// прогонит `xray run -test` и вернёт настоящую причину.
		aliveCtx, aliveCancel := context.WithTimeout(ctx, 20*time.Second)
		aliveErr := a.core.WaitAlive(aliveCtx, 400*time.Millisecond)
		aliveCancel()
		if aliveErr != nil {
			return fail(aliveErr)
		}

		if mode != "tun" {
			break
		}
		a.mu.Lock()
		a.state.Connection.StatusMessage = "Создаём сетевой адаптер…"
		a.mu.Unlock()
		tunErr := a.waitTunReady(ctx, settings.TestURL)
		if tunErr == nil {
			a.logf("туннель поднят, имя адаптера %s", tunName)
			break
		}
		a.logf("адаптер %s не заработал: %v", tunName, tunErr)
		_ = a.core.Stop()
		if attempt == len(tunNames)-1 {
			// Сетевой адаптер может не подниматься по причинам вне программы:
			// занятый Wintun, антивирус, следы прошлых запусков. Оставлять
			// человека совсем без связи из-за этого неправильно — переходим
			// на системный прокси, который прав не требует и работает всегда.
			a.logf("TUN не поднялся, перехожу на системный прокси")
			mode = "system-proxy"
			fallbackNote = "Сетевой адаптер не поднялся — подключено через системный прокси. " +
				"Для TUN закройте другие VPN-приложения и перезагрузите компьютер"
			httpPort = xray.FreeLocalPort(10809)
			socksPort = xray.FreeLocalPort(10808)
			if socksPort == httpPort {
				socksPort = xray.FreeLocalPort(httpPort + 1)
			}
			a.portMu.Lock()
			a.httpPort, a.socksPort = httpPort, socksPort
			a.portMu.Unlock()

			built, buildErr := xray.Build(node, xray.ConfigOptions{
				Mode: mode, HTTPPort: httpPort, SOCKSPort: socksPort,
				DNS1: settings.DNS1, DNS2: settings.DNS2,
				BlockAds:    settings.BlockAds,
				AccessLog:   filepath.Join(a.core.RuntimeDir(), "access.log"),
				ErrorLog:    filepath.Join(a.core.RuntimeDir(), "error.log"),
				GeoDir:      a.core.CoreDir(),
				APIPort:     apiPort,
				EnableStats: true,
			})
			if buildErr != nil {
				return fail(buildErr)
			}
			warnings = built.Warnings
			if startErr := a.core.Start(built.Config, mode); startErr != nil {
				return fail(startErr)
			}
			aliveCtx, aliveCancel := context.WithTimeout(ctx, 20*time.Second)
			aliveErr := a.core.WaitAlive(aliveCtx, 400*time.Millisecond)
			aliveCancel()
			if aliveErr != nil {
				return fail(aliveErr)
			}
			break
		}
	}

	if mode == "system-proxy" {
		portCtx, portCancel := context.WithTimeout(ctx, 7*time.Second)
		err = xray.WaitLocalPort(portCtx, httpPort, 6*time.Second)
		portCancel()
		if err != nil {
			return fail(fmt.Errorf("Xray запущен, но HTTP proxy не открылся: %w", err))
		}

		// Реальный запрос через Xray до того, как трогать настройки Windows.
		testCtx, testCancel := context.WithTimeout(ctx, 18*time.Second)
		err = xray.VerifyHTTPProxy(testCtx, httpPort, settings.TestURL)
		testCancel()
		if err != nil {
			return fail(fmt.Errorf("сервер не прошёл контроль соединения через Xray: %w", err))
		}

		if err := EnableSystemProxy(a.dataDir, httpPort, socksPort); err != nil {
			return fail(fmt.Errorf("не удалось включить системный proxy: %w", err))
		}
	}
	// Для TUN отдельная проверка не нужна: waitTunReady уже убедился, что
	// трафик реально идёт через адаптер.

	a.mu.Lock()
	if fallbackNote != "" {
		warnings = append([]string{fallbackNote}, warnings...)
		// Запоминаем рабочий режим, чтобы следующее подключение не тратило
		// снова полминуты на заведомо неудачную попытку поднять адаптер.
		a.state.Settings.ConnectionMode = mode
	}
	a.state.Connection = model.ConnectionState{
		Connected: true, NodeID: node.ID, NodeName: node.Name, Mode: mode,
		StartedAt: time.Now(), StatusMessage: "Подключено",
		Warnings: warnings,
	}
	if mode == "system-proxy" {
		a.state.Connection.LocalHTTPProxy = fmt.Sprintf("127.0.0.1:%d", httpPort)
		a.state.Connection.LocalSOCKS = fmt.Sprintf("127.0.0.1:%d", socksPort)
	}
	a.state.SelectedNodeID = node.ID
	a.state.SelectedSubID = subID
	a.state.RecentNodeIDs = prependUnique(a.state.RecentNodeIDs, node.ID, 20)
	a.logf("подключено: %s (%s)", node.Name, mode)
	_ = a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	return state, nil
}

// waitTunReady ждёт, пока туннель действительно заработает.
//
// Единственный надёжный признак — прошедший контрольный запрос. Привязка к
// строкам журнала Xray оказалась негодной: «Creating adapter» появляется в
// момент начала создания адаптера, а сообщения об успехе может не быть вовсе.
func (a *App) waitTunReady(ctx context.Context, testURL string) error {
	// Адаптеру и маршрутам нужно время появиться в системе.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1200 * time.Millisecond):
	}

	deadline := time.Now().Add(20 * time.Second)
	var lastProbeErr error
	for time.Now().Before(deadline) {
		log := a.core.TailLog(80)
		if failure := tunFailure(log); failure != nil {
			return failure
		}
		if !a.core.Running() {
			return fmt.Errorf("Xray остановился при создании адаптера:\n%s", tailLines(log, 8))
		}

		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		lastProbeErr = xray.VerifySystemPath(probeCtx, testURL)
		cancel()
		if lastProbeErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if lastProbeErr != nil {
		return fmt.Errorf("адаптер поднят, но трафик через него не пошёл: %w", lastProbeErr)
	}
	return errors.New("не удалось поднять туннель за 20 секунд")
}

// tunFailure распознаёт типовые причины, по которым Wintun не создаётся.
func tunFailure(log string) error {
	lower := strings.ToLower(log)
	switch {
	case strings.Contains(log, "Failed to setup adapter"),
		strings.Contains(lower, "cannot create a file when that file already exists"),
		strings.Contains(lower, "уже существует"):
		return errors.New("сетевой адаптер занят: Windows не дал создать Wintun-адаптер")
	case strings.Contains(lower, "access is denied"), strings.Contains(lower, "отказано в доступе"):
		return errors.New("не хватает прав администратора для TUN-режима")
	}
	return nil
}

func tailLines(text string, count int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}

func (a *App) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	state, err := a.Disconnect()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, state)
}

func (a *App) Disconnect() (model.AppState, error) {
	a.logf("отключение")
	a.abortConnect()
	a.statsMu.Lock()
	if a.statsClient != nil {
		a.statsClient.Reset()
	}
	a.statsMu.Unlock()
	coreErr := a.core.Stop()
	proxyErr := RestoreSystemProxy(a.dataDir)
	a.mu.Lock()
	a.state.Connection = model.ConnectionState{StatusMessage: "Отключено"}
	state := a.state
	a.mu.Unlock()
	if coreErr != nil {
		return state, &APIError{Code: "DISCONNECT_FAILED", Message: coreErr.Error()}
	}
	if proxyErr != nil {
		return state, &APIError{Code: "PROXY_RESTORE_FAILED", Message: proxyErr.Error()}
	}
	return state, nil
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var settings model.Settings
	if err := readJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings = normalizeSettings(settings)
	a.mu.Lock()
	if a.state.Connection.Connected && settings.ConnectionMode != a.state.Settings.ConnectionMode {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, &APIError{Code: "DISCONNECT_FIRST", Message: "Сначала отключите VPN, затем меняйте режим"})
		return
	}
	a.state.Settings = settings
	_ = a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	writeOK(w, state)
}

func (a *App) handleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	parts := []string{}
	if appLog := a.AppLog(60); appLog != "" {
		parts = append(parts, "=== MS7VPN ===\n"+appLog)
	}
	if coreLog := a.core.TailLog(100); coreLog != "" {
		parts = append(parts, "=== Xray ===\n"+coreLog)
	}
	writeOK(w, map[string]any{"log": strings.Join(parts, "\n\n")})
}

type openRequest struct {
	URL string `json:"url"`
}

func (a *App) handleOpenURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request openRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !strings.HasPrefix(request.URL, "https://") && !strings.HasPrefix(request.URL, "http://") {
		writeError(w, http.StatusBadRequest, &APIError{Code: "INVALID_URL", Message: "Разрешены только http/https ссылки"})
		return
	}
	if err := OpenExternalURL(request.URL); err != nil {
		writeError(w, http.StatusInternalServerError, &APIError{Code: "OPEN_FAILED", Message: err.Error()})
		return
	}
	writeOK(w, map[string]bool{"opened": true})
}

func (a *App) handleOpenDataFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	if err := OpenFolder(a.dataDir); err != nil {
		writeError(w, http.StatusInternalServerError, &APIError{Code: "OPEN_FOLDER_FAILED", Message: err.Error()})
		return
	}
	writeOK(w, map[string]bool{"opened": true})
}

func (a *App) handleElevate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request nodeIDRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if IsAdministrator() {
		writeOK(w, map[string]bool{"alreadyAdministrator": true})
		return
	}
	if err := RelaunchElevated(request.NodeID); err != nil {
		writeError(w, http.StatusInternalServerError, &APIError{Code: "ELEVATION_FAILED", Message: err.Error()})
		return
	}
	writeOK(w, map[string]bool{"relaunching": true})
	go func() {
		time.Sleep(600 * time.Millisecond)
		a.Shutdown()
	}()
}

// handleCheckUpdate спрашивает сервер обновлений о последней сборке.
//
// Ничего не скачивается: приложение только сообщает, есть ли версия новее,
// и даёт ссылку. Устанавливать или нет — решает человек.
func (a *App) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	settings := a.Snapshot().Settings
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	result, err := update.Check(ctx, settings.UpdateURL, model.AppVersion)
	if err != nil {
		a.logf("проверка обновлений: %v", err)
		writeError(w, http.StatusBadGateway, &APIError{Code: "UPDATE_CHECK_FAILED", Message: err.Error()})
		return
	}
	a.logf("проверка обновлений: установлено %s, доступно %s", result.Current, result.Latest)
	writeOK(w, result)
}

func (a *App) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	writeOK(w, map[string]bool{"closing": true})
	go func() {
		time.Sleep(150 * time.Millisecond)
		a.Shutdown()
	}()
}

func (a *App) Shutdown() {
	a.quit.Do(func() {
		_, _ = a.Disconnect()
		close(a.done)
	})
}

func (a *App) findNodeLocked(nodeID string) (model.Node, string, bool) {
	for _, sub := range a.state.Subscriptions {
		for _, node := range sub.Nodes {
			if node.ID == nodeID {
				return node, sub.ID, true
			}
		}
	}
	return model.Node{}, "", false
}

func (a *App) nodeExistsLocked(nodeID string) bool {
	if nodeID == "" {
		return false
	}
	_, _, found := a.findNodeLocked(nodeID)
	return found
}

func (a *App) firstSupportedNodeLocked() string {
	for _, sub := range a.state.Subscriptions {
		if id := firstSupportedNodeID(sub.Nodes); id != "" {
			return id
		}
	}
	return ""
}

func firstSupportedNodeID(nodes []model.Node) string {
	for _, node := range nodes {
		if node.Supported {
			return node.ID
		}
	}
	return ""
}

func preserveNodeRuntime(destination *model.Subscription, source model.Subscription) {
	old := make(map[string]model.Node, len(source.Nodes))
	for _, node := range source.Nodes {
		old[node.ID] = node
	}
	for index := range destination.Nodes {
		if previous, ok := old[destination.Nodes[index].ID]; ok {
			destination.Nodes[index].PingMS = previous.PingMS
			destination.Nodes[index].PingStatus = previous.PingStatus
			destination.Nodes[index].LastPingAt = previous.LastPingAt
		}
	}
}

func nodeInSubscription(sub model.Subscription, nodeID string) bool {
	for _, node := range sub.Nodes {
		if node.ID == nodeID {
			return true
		}
	}
	return false
}

func prependUnique(values []string, value string, limit int) []string {
	result := []string{value}
	for _, existing := range values {
		if existing != value {
			result = append(result, existing)
		}
		if len(result) >= limit {
			break
		}
	}
	return result
}

// normalizeSettings — единое место, где подставляются значения по умолчанию.
// Раньше HTTP-обработчик и метод для нативного окна подставляли РАЗНЫЙ режим
// по умолчанию (system-proxy против tun), и одинаковый ввод давал разный
// результат в зависимости от того, откуда пришли настройки.
func normalizeSettings(settings model.Settings) model.Settings {
	defaults := model.DefaultSettings()
	if settings.ConnectionMode != "tun" && settings.ConnectionMode != "system-proxy" {
		settings.ConnectionMode = defaults.ConnectionMode
	}
	if strings.TrimSpace(settings.DNS1) == "" {
		settings.DNS1 = defaults.DNS1
	}
	if strings.TrimSpace(settings.DNS2) == "" {
		settings.DNS2 = defaults.DNS2
	}
	if strings.TrimSpace(settings.TestURL) == "" {
		settings.TestURL = defaults.TestURL
	}
	if strings.TrimSpace(settings.SupportURL) == "" {
		settings.SupportURL = defaults.SupportURL
	}
	if strings.TrimSpace(settings.UpdateURL) == "" {
		settings.UpdateURL = defaults.UpdateURL
	}
	return settings
}

func methodError() *APIError {
	return &APIError{Code: "METHOD_NOT_ALLOWED", Message: "Метод не поддерживается"}
}

func readJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return &APIError{Code: "INVALID_JSON", Message: "Некорректные данные: " + err.Error()}
	}
	return nil
}

func writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	apiErr := &APIError{Code: "ERROR", Message: err.Error()}
	if errors.As(err, &apiErr) {
		// keep typed error
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": apiErr})
}

// SortNodes returns a stable UI order: favorites first, then healthy ping,
// then the original subscription order. It is used by tests and can be reused
// by a future native UI without changing persisted data.
func SortNodes(nodes []model.Node, favorites map[string]bool) []model.Node {
	result := append([]model.Node(nil), nodes...)
	sort.SliceStable(result, func(i, j int) bool {
		fi, fj := favorites[result[i].ID], favorites[result[j].ID]
		if fi != fj {
			return fi
		}
		pi, pj := result[i].PingMS, result[j].PingMS
		if pi > 0 && pj <= 0 {
			return true
		}
		if pj > 0 && pi <= 0 {
			return false
		}
		if pi > 0 && pj > 0 && pi != pj {
			return pi < pj
		}
		return false
	})
	return result
}

func (a *App) DebugStatePath() string { return filepath.Join(a.dataDir, "state.json") }
func (a *App) CoreLogPath() string    { return filepath.Join(a.dataDir, "runtime", "xray-process.log") }
func (a *App) CurrentExecutable() string {
	path, _ := os.Executable()
	return path
}
