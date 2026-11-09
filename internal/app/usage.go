package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Статистика: трафик, время в VPN и подключения по дням.
//
// Хранится отдельно от state.json: она пишется раз в полминуты, пока VPN
// включён, и гонять ради неё весь файл состояния с подписками незачем.
// Это украшение — ошибки чтения и записи никуда не всплывают.

const usageKeepDays = 90

type usageDay struct {
	Up          int64 `json:"up"`
	Down        int64 `json:"down"`
	Seconds     int64 `json:"seconds"`
	Connections int   `json:"connections"`
	PingSum     int64 `json:"pingSum,omitempty"`
	PingCount   int64 `json:"pingCount,omitempty"`
}

type usageBook struct {
	mu   sync.Mutex
	path string
	days map[string]*usageDay

	// Счётчики ядра идут с начала сеанса. Запоминаем прошлое значение,
	// чтобы складывать в день только прирост.
	lastUp, lastDown int64
	lastTick         time.Time
	dirty            bool
	savedAt          time.Time
}

func loadUsage(dataDir string) *usageBook {
	book := &usageBook{path: filepath.Join(dataDir, "usage.json"), days: map[string]*usageDay{}}
	if data, err := os.ReadFile(book.path); err == nil {
		_ = json.Unmarshal(data, &book.days)
		if book.days == nil {
			book.days = map[string]*usageDay{}
		}
	}
	return book
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

func (b *usageBook) today() *usageDay {
	key := dayKey(time.Now())
	day := b.days[key]
	if day == nil {
		day = &usageDay{}
		b.days[key] = day
	}
	return day
}

// startSession — новое подключение: счётчики ядра начнутся с нуля.
func (b *usageBook) startSession() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.today().Connections++
	b.lastUp, b.lastDown = 0, 0
	b.lastTick = time.Now()
	b.dirty = true
}

// addTraffic складывает прирост счётчиков и прошедшее время в текущий день.
func (b *usageBook) addTraffic(up, down int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	day := b.today()
	deltaUp, deltaDown := up-b.lastUp, down-b.lastDown
	// Счётчик сбросился (переподключение внутри ядра) — весь он и есть прирост.
	if deltaUp < 0 {
		deltaUp = up
	}
	if deltaDown < 0 {
		deltaDown = down
	}
	day.Up += deltaUp
	day.Down += deltaDown
	b.lastUp, b.lastDown = up, down
	now := time.Now()
	if !b.lastTick.IsZero() {
		elapsed := now.Sub(b.lastTick)
		// Сон компьютера или зависший опрос — не время в VPN.
		if elapsed > 0 && elapsed < 10*time.Second {
			day.Seconds += int64(elapsed.Round(time.Second) / time.Second)
		}
	}
	b.lastTick = now
	b.dirty = true
}

func (b *usageBook) addPing(ms int) {
	if ms <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	day := b.today()
	day.PingSum += int64(ms)
	day.PingCount++
	b.dirty = true
}

// stopSession — соединение закрыто: следующее время считаем с нового тика.
func (b *usageBook) stopSession() {
	b.mu.Lock()
	b.lastTick = time.Time{}
	b.mu.Unlock()
}

// maybeSave пишет файл не чаще раза в 30 секунд; force — при выходе.
func (b *usageBook) maybeSave(force bool) {
	b.mu.Lock()
	if !b.dirty || (!force && time.Since(b.savedAt) < 30*time.Second) {
		b.mu.Unlock()
		return
	}
	cutoff := dayKey(time.Now().AddDate(0, 0, -usageKeepDays))
	for key := range b.days {
		if key < cutoff {
			delete(b.days, key)
		}
	}
	data, err := json.Marshal(b.days)
	b.dirty = false
	b.savedAt = time.Now()
	path := b.path
	b.mu.Unlock()
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (b *usageBook) reset() {
	b.mu.Lock()
	b.days = map[string]*usageDay{}
	b.dirty = true
	b.savedAt = time.Time{}
	b.mu.Unlock()
	b.maybeSave(true)
}

type usageDayView struct {
	Date        string `json:"date"`
	Up          int64  `json:"up"`
	Down        int64  `json:"down"`
	Seconds     int64  `json:"seconds"`
	Connections int    `json:"connections"`
	AvgPing     int    `json:"avgPing,omitempty"`
}

// view — последние n дней по порядку, включая пустые: на графике пропуски
// должны быть видны, а не схлопываться.
func (b *usageBook) view(n int) []usageDayView {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]usageDayView, 0, n)
	now := time.Now()
	for i := n - 1; i >= 0; i-- {
		key := dayKey(now.AddDate(0, 0, -i))
		item := usageDayView{Date: key}
		if day := b.days[key]; day != nil {
			item.Up, item.Down, item.Seconds, item.Connections = day.Up, day.Down, day.Seconds, day.Connections
			if day.PingCount > 0 {
				item.AvgPing = int(day.PingSum / day.PingCount)
			}
		}
		out = append(out, item)
	}
	return out
}

// totals — всё время, что хранится.
func (b *usageBook) totals() usageDayView {
	b.mu.Lock()
	defer b.mu.Unlock()
	var total usageDayView
	var pingSum, pingCount int64
	keys := make([]string, 0, len(b.days))
	for key := range b.days {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		day := b.days[key]
		total.Up += day.Up
		total.Down += day.Down
		total.Seconds += day.Seconds
		total.Connections += day.Connections
		pingSum += day.PingSum
		pingCount += day.PingCount
	}
	if len(keys) > 0 {
		total.Date = keys[0]
	}
	if pingCount > 0 {
		total.AvgPing = int(pingSum / pingCount)
	}
	return total
}

func (a *App) recordConnection() { a.usage.startSession() }

func (a *App) handleUsage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeOK(w, map[string]any{"days": a.usage.view(30), "total": a.usage.totals()})
	case http.MethodDelete:
		a.usage.reset()
		writeOK(w, map[string]bool{"cleared": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, methodError())
	}
}
