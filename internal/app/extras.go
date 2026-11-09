package app

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Дополнительные возможности окна: свои имена серверов, ссылки ms7vpn://,
// автозапуск с Windows и список запущенных программ для «Без VPN».

func (a *App) registerExtraRoutes(mux *http.ServeMux, withAuth func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/api/nodes/rename", withAuth(a.handleRenameNode))
	mux.HandleFunc("/api/deeplink/take", withAuth(a.handleTakeDeepLink))
	mux.HandleFunc("/api/autostart", withAuth(a.handleAutostart))
	mux.HandleFunc("/api/processes", withAuth(a.handleProcesses))
	mux.HandleFunc("/api/appicon", withAuth(a.handleAppIcon))
	mux.HandleFunc("/api/subscriptions/move", withAuth(a.handleMoveSubscription))
	mux.HandleFunc("/api/subscriptions/select", withAuth(a.handleSelectSubscription))
}

type renameRequest struct {
	NodeID string `json:"nodeId"`
	Name   string `json:"name"`
}

// handleRenameNode задаёт своё имя серверу. Пустое имя возвращает имя из
// подписки.
func (a *App) handleRenameNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request renameRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.Join(strings.Fields(request.Name), " ")
	if utf8.RuneCountInString(name) > 60 {
		name = string([]rune(name)[:60])
	}
	a.mu.Lock()
	if !a.nodeExistsLocked(request.NodeID) {
		a.mu.Unlock()
		writeError(w, http.StatusNotFound, &APIError{Code: "NODE_NOT_FOUND", Message: "Сервер не найден"})
		return
	}
	if a.state.NodeNames == nil {
		a.state.NodeNames = map[string]string{}
	}
	if name == "" {
		delete(a.state.NodeNames, request.NodeID)
	} else {
		a.state.NodeNames[request.NodeID] = name
	}
	saveErr := a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, saveErr)
		return
	}
	writeOK(w, state)
}

// ==== ссылки ms7vpn:// ====
//
// Кнопка в боте открывает ms7vpn://add?url=<подписка>. Windows запускает
// программу с этой ссылкой; если копия уже работает, новая записывает ссылку
// в файл и будит старую, а окно забирает ссылку при очередном опросе и
// спрашивает человека, добавить ли подписку. Без вопроса любая страница в
// браузере могла бы подсунуть свою подписку.

const URLScheme = "ms7vpn"

func pendingLinkPath(dataDir string) string { return filepath.Join(dataDir, "pending-link") }

// SavePendingLink запоминает ссылку для окна. Вызывается из main до
// проверки второй копии.
func SavePendingLink(dataDir, link string) error {
	if _, ok := SubscriptionFromDeepLink(link); !ok {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(pendingLinkPath(dataDir), []byte(link), 0o600)
}

// SubscriptionFromDeepLink достаёт адрес подписки из ссылки вида
// ms7vpn://add?url=…, ms7vpn://import/<адрес> или ms7vpn://<адрес>.
func SubscriptionFromDeepLink(link string) (string, bool) {
	link = strings.TrimSpace(link)
	prefix := URLScheme + "://"
	if !strings.HasPrefix(strings.ToLower(link), prefix) {
		return "", false
	}
	rest := link[len(prefix):]
	var candidate string
	if parsed, err := url.Parse(link); err == nil && parsed.Query().Get("url") != "" {
		candidate = parsed.Query().Get("url")
	} else {
		for _, head := range []string{"add/", "import/", "install-config/", "subscription/"} {
			if strings.HasPrefix(strings.ToLower(rest), head) {
				rest = rest[len(head):]
				break
			}
		}
		if decoded, err := url.PathUnescape(rest); err == nil {
			rest = decoded
		}
		candidate = rest
	}
	candidate = strings.TrimSpace(candidate)
	parsed, err := url.Parse(candidate)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || len(candidate) > 2048 {
		return "", false
	}
	return candidate, true
}

func (a *App) handleTakeDeepLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	path := pendingLinkPath(a.dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		writeOK(w, map[string]string{})
		return
	}
	_ = os.Remove(path)
	sub, ok := SubscriptionFromDeepLink(string(data))
	if !ok {
		writeOK(w, map[string]string{})
		return
	}
	a.logf("получена ссылка ms7vpn:// на подписку")
	writeOK(w, map[string]string{"url": sub})
}

// ==== автозапуск ====

type autostartRequest struct {
	Enabled bool `json:"enabled"`
}

func (a *App) handleAutostart(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeOK(w, map[string]bool{"enabled": AutostartEnabled()})
	case http.MethodPost:
		var request autostartRequest
		if err := readJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := SetAutostart(request.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, &APIError{Code: "AUTOSTART_FAILED", Message: "Не удалось изменить автозапуск: " + err.Error()})
			return
		}
		writeOK(w, map[string]bool{"enabled": AutostartEnabled()})
	default:
		writeError(w, http.StatusMethodNotAllowed, methodError())
	}
}

// ==== запущенные программы ====

func (a *App) handleProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	writeOK(w, map[string][]string{"processes": runningPrograms()})
}

// handleAppIcon отдаёт значок программы в PNG: ?name=telegram.exe или полный
// путь. 404 — программа не найдена или без значка, окно покажет запасной.
func (a *App) handleAppIcon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" || len(name) > 400 {
		http.NotFound(w, r)
		return
	}
	data := appIconPNG(name)
	if len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=600")
	_, _ = w.Write(data)
}

type moveRequest struct {
	ID    string `json:"id"`
	Delta int    `json:"delta"`
}

// handleMoveSubscription меняет порядок подписок: delta -1 — выше, +1 — ниже.
// Порядок виден в «Подписках», в списке серверов и решает, какая подписка
// показывается на главной, когда сервер не выбран.
func (a *App) handleMoveSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request moveRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.mu.Lock()
	subs := a.state.Subscriptions
	index := -1
	for i, sub := range subs {
		if sub.ID == request.ID {
			index = i
			break
		}
	}
	target := index + request.Delta
	if index < 0 || (request.Delta != -1 && request.Delta != 1) || target < 0 || target >= len(subs) {
		state := a.state
		a.mu.Unlock()
		writeOK(w, state)
		return
	}
	subs[index], subs[target] = subs[target], subs[index]
	saveErr := a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, saveErr)
		return
	}
	writeOK(w, state)
}

type selectSubscriptionRequest struct {
	ID string `json:"id"`
}

// handleSelectSubscription делает подписку активной: её серверы в списке, её
// данные на главной. Если выбранный сервер из другой подписки — выбирается
// первый рабочий сервер этой.
func (a *App) handleSelectSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	var request selectSubscriptionRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, a.SelectSubscription(request.ID))
}
