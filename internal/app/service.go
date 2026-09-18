package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"ms7vpn/internal/model"
	"ms7vpn/internal/subscription"
	"ms7vpn/internal/xray"
)

// AddSubscription imports a Remnawave/Happ-compatible subscription or a direct share link.
// It is used by the native Windows UI and therefore does not depend on the legacy local HTTP UI.
func (a *App) AddSubscription(ctx context.Context, rawURL, name string) (model.AppState, error) {
	result, err := a.fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return a.Snapshot(), &APIError{Code: "SUBSCRIPTION_IMPORT_FAILED", Message: err.Error()}
	}
	if len(result.Nodes) == 0 {
		return a.Snapshot(), &APIError{Code: "EMPTY_SUBSCRIPTION", Message: "В подписке не найдено поддерживаемых серверов"}
	}
	if len(result.Nodes) > 5000 {
		return a.Snapshot(), &APIError{Code: "TOO_MANY_NODES", Message: "В одной подписке больше 5000 серверов"}
	}

	now := time.Now()
	id := subscription.StableID(rawURL)
	if name == "" {
		name = result.Name
	}
	item := model.Subscription{
		ID:                    id,
		Name:                  name,
		URL:                   rawURL,
		Nodes:                 result.Nodes,
		UpdatedAt:             now,
		CreatedAt:             now,
		UpdateIntervalMinutes: result.UpdateIntervalMinutes,
		UserInfo:              result.UserInfo,
		Warnings:              result.Warnings,
	}

	a.mu.Lock()
	replaced := false
	for i := range a.state.Subscriptions {
		if a.state.Subscriptions[i].ID == id || a.state.Subscriptions[i].URL == rawURL {
			item.CreatedAt = a.state.Subscriptions[i].CreatedAt
			preserveNodeRuntime(&item, a.state.Subscriptions[i])
			a.state.Subscriptions[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		if len(a.state.Subscriptions) >= 500 {
			a.mu.Unlock()
			return a.Snapshot(), &APIError{Code: "TOO_MANY_SUBSCRIPTIONS", Message: "Лимит: 500 подписок"}
		}
		a.state.Subscriptions = append(a.state.Subscriptions, item)
	}
	a.state.SelectedSubID = id
	if a.state.SelectedNodeID == "" || !a.nodeExistsLocked(a.state.SelectedNodeID) {
		a.state.SelectedNodeID = firstSupportedNodeID(item.Nodes)
	}
	if result.FinalURL != "" && result.FinalURL != rawURL {
		for i := range a.state.Subscriptions {
			if a.state.Subscriptions[i].ID == id {
				a.state.Subscriptions[i].URL = result.FinalURL
				break
			}
		}
	}
	saveErr := a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	if saveErr != nil {
		return state, &APIError{Code: "SAVE_FAILED", Message: saveErr.Error()}
	}
	return state, nil
}

func (a *App) RefreshSubscription(ctx context.Context, id string) (model.AppState, error) {
	a.mu.RLock()
	var current model.Subscription
	found := false
	for _, sub := range a.state.Subscriptions {
		if sub.ID == id {
			current = sub
			found = true
			break
		}
	}
	a.mu.RUnlock()
	if !found {
		return a.Snapshot(), &APIError{Code: "SUBSCRIPTION_NOT_FOUND", Message: "Подписка не найдена"}
	}

	result, err := a.fetcher.Fetch(ctx, current.URL)
	if err != nil {
		a.mu.Lock()
		for i := range a.state.Subscriptions {
			if a.state.Subscriptions[i].ID == id {
				a.state.Subscriptions[i].LastError = err.Error()
			}
		}
		_ = a.store.Save(a.state)
		state := a.state
		a.mu.Unlock()
		return state, &APIError{Code: "SUBSCRIPTION_REFRESH_FAILED", Message: err.Error()}
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
	if result.FinalURL != "" {
		updated.URL = result.FinalURL
	}
	preserveNodeRuntime(&updated, current)

	a.mu.Lock()
	for i := range a.state.Subscriptions {
		if a.state.Subscriptions[i].ID == id {
			a.state.Subscriptions[i] = updated
			break
		}
	}
	if !a.nodeExistsLocked(a.state.SelectedNodeID) {
		a.state.SelectedNodeID = firstSupportedNodeID(updated.Nodes)
	}
	saveErr := a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	if saveErr != nil {
		return state, &APIError{Code: "SAVE_FAILED", Message: saveErr.Error()}
	}
	return state, nil
}

func (a *App) DeleteSubscription(id string) (model.AppState, error) {
	a.mu.Lock()
	index := -1
	for i, sub := range a.state.Subscriptions {
		if sub.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		a.mu.Unlock()
		return a.Snapshot(), &APIError{Code: "SUBSCRIPTION_NOT_FOUND", Message: "Подписка не найдена"}
	}
	deleted := a.state.Subscriptions[index]
	if nodeInSubscription(deleted, a.state.Connection.NodeID) && (a.state.Connection.Connected || a.state.Connection.Connecting) {
		a.mu.Unlock()
		return a.Snapshot(), &APIError{Code: "SUBSCRIPTION_IN_USE", Message: "Сначала отключите VPN"}
	}
	for _, node := range deleted.Nodes {
		delete(a.state.Favorites, node.ID)
	}
	a.state.Subscriptions = append(a.state.Subscriptions[:index], a.state.Subscriptions[index+1:]...)
	if a.state.SelectedSubID == id {
		a.state.SelectedSubID = ""
		if len(a.state.Subscriptions) > 0 {
			a.state.SelectedSubID = a.state.Subscriptions[0].ID
		}
	}
	if !a.nodeExistsLocked(a.state.SelectedNodeID) {
		a.state.SelectedNodeID = a.firstSupportedNodeLocked()
	}
	saveErr := a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	if saveErr != nil {
		return state, &APIError{Code: "SAVE_FAILED", Message: saveErr.Error()}
	}
	return state, nil
}

func (a *App) SelectSubscription(id string) model.AppState {
	a.mu.Lock()
	for _, sub := range a.state.Subscriptions {
		if sub.ID == id {
			a.state.SelectedSubID = id
			if !nodeInSubscription(sub, a.state.SelectedNodeID) {
				a.state.SelectedNodeID = firstSupportedNodeID(sub.Nodes)
			}
			_ = a.store.Save(a.state)
			break
		}
	}
	state := a.state
	a.mu.Unlock()
	return state
}

func (a *App) SelectNode(id string) (model.AppState, error) {
	a.mu.Lock()
	node, subID, found := a.findNodeLocked(id)
	if !found {
		a.mu.Unlock()
		return a.Snapshot(), &APIError{Code: "NODE_NOT_FOUND", Message: "Сервер не найден"}
	}
	if !node.Supported {
		a.mu.Unlock()
		return a.Snapshot(), &APIError{Code: "NODE_UNSUPPORTED", Message: node.UnsupportedReason}
	}
	a.state.SelectedNodeID = node.ID
	a.state.SelectedSubID = subID
	_ = a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	return state, nil
}

func (a *App) ToggleFavorite(id string) (bool, error) {
	a.mu.Lock()
	if !a.nodeExistsLocked(id) {
		a.mu.Unlock()
		return false, &APIError{Code: "NODE_NOT_FOUND", Message: "Сервер не найден"}
	}
	a.state.Favorites[id] = !a.state.Favorites[id]
	value := a.state.Favorites[id]
	_ = a.store.Save(a.state)
	a.mu.Unlock()
	return value, nil
}

func (a *App) PingNodes(ctx context.Context, subscriptionID string, nodeIDs []string) (model.AppState, error) {
	a.mu.RLock()
	wanted := make(map[string]bool)
	for _, id := range nodeIDs {
		wanted[id] = true
	}
	var targets []model.Node
	for _, sub := range a.state.Subscriptions {
		if subscriptionID != "" && sub.ID != subscriptionID {
			continue
		}
		for _, node := range sub.Nodes {
			if len(wanted) == 0 || wanted[node.ID] {
				targets = append(targets, node)
			}
		}
	}
	a.mu.RUnlock()
	if len(targets) == 0 {
		return a.Snapshot(), &APIError{Code: "NO_NODES", Message: "Нет серверов для проверки"}
	}
	if len(targets) > 500 {
		targets = targets[:500]
	}

	type pingResult struct {
		id     string
		ms     int
		status string
	}
	jobs := make(chan model.Node)
	results := make(chan pingResult, len(targets))
	workers := 12
	if len(targets) < workers {
		workers = len(targets)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for node := range jobs {
				child, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
				duration, err := xray.PingTCP(child, node.Address, node.Port, 3*time.Second)
				cancel()
				if err != nil {
					results <- pingResult{id: node.ID, ms: -1, status: "error"}
				} else {
					ms := int(duration.Round(time.Millisecond) / time.Millisecond)
					if ms < 1 {
						ms = 1
					}
					results <- pingResult{id: node.ID, ms: ms, status: "ok"}
				}
			}
		}()
	}
	go func() {
		for _, node := range targets {
			select {
			case jobs <- node:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				close(results)
				return
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	resultMap := map[string]pingResult{}
	for r := range results {
		resultMap[r.id] = r
	}
	if err := ctx.Err(); err != nil && len(resultMap) == 0 {
		return a.Snapshot(), fmt.Errorf("проверка пинга отменена: %w", err)
	}
	now := time.Now()
	a.mu.Lock()
	for si := range a.state.Subscriptions {
		for ni := range a.state.Subscriptions[si].Nodes {
			n := &a.state.Subscriptions[si].Nodes[ni]
			if r, ok := resultMap[n.ID]; ok {
				n.PingMS, n.PingStatus, n.LastPingAt = r.ms, r.status, now
			}
		}
	}
	_ = a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	return state, nil
}

func (a *App) SaveSettings(settings model.Settings) (model.AppState, error) {
	settings = normalizeSettings(settings)
	a.mu.Lock()
	if a.state.Connection.Connected && settings.ConnectionMode != a.state.Settings.ConnectionMode {
		a.mu.Unlock()
		return a.Snapshot(), &APIError{Code: "DISCONNECT_FIRST", Message: "Сначала отключите VPN, затем меняйте режим"}
	}
	a.state.Settings = settings
	saveErr := a.store.Save(a.state)
	state := a.state
	a.mu.Unlock()
	if saveErr != nil {
		return state, saveErr
	}
	return state, nil
}

func (a *App) CoreLog(lines int) string { return a.core.TailLog(lines) }

// StartBackgroundTasks запускает фоновое обновление подписок.
//
// Галочка «Обновлять подписки автоматически» до этого существовала только в
// настройках и в модели: в ветке с окном WebView2 её никто не читал, и
// подписки обновлялись исключительно вручную.
func (a *App) StartBackgroundTasks() {
	go a.autoRefreshLoop()
	go a.trafficLoop()
}

// trafficLoop раз в секунду читает счётчики Xray и обновляет скорость.
//
// Ошибки опроса намеренно игнорируются: статистика — это украшение, и она не
// должна влиять ни на состояние подключения, ни на журнал.
func (a *App) trafficLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
		}

		a.mu.RLock()
		connected := a.state.Connection.Connected
		a.mu.RUnlock()

		a.statsMu.Lock()
		client := a.statsClient
		a.statsMu.Unlock()
		if !connected || client == nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		traffic, err := client.Query(ctx)
		cancel()
		if err != nil {
			continue
		}

		a.mu.Lock()
		if a.state.Connection.Connected {
			a.state.Connection.UplinkBytes = traffic.UplinkBytes
			a.state.Connection.DownlinkBytes = traffic.DownlinkBytes
			a.state.Connection.UplinkBitsSec = traffic.UplinkBitsSec
			a.state.Connection.DownlinkBitsSec = traffic.DownlinkBitsSec
		}
		a.mu.Unlock()
	}
}

func (a *App) autoRefreshLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
		}
		snapshot := a.Snapshot()
		if !snapshot.Settings.AutoRefresh {
			continue
		}
		for _, sub := range snapshot.Subscriptions {
			interval := sub.UpdateIntervalMinutes
			if interval <= 0 {
				interval = defaultRefreshMinutes
			}
			if time.Since(sub.UpdatedAt) < time.Duration(interval)*time.Minute {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			_, _ = a.RefreshSubscription(ctx, sub.ID)
			cancel()
			select {
			case <-a.done:
				return
			default:
			}
		}
	}
}

const defaultRefreshMinutes = 360

// AutoConnectNodeID возвращает сервер, к которому нужно подключиться сразу
// после запуска, если включена соответствующая настройка. Пустая строка —
// подключаться не нужно.
func (a *App) AutoConnectNodeID() string {
	snapshot := a.Snapshot()
	if !snapshot.Settings.AutoConnect {
		return ""
	}
	if snapshot.SelectedNodeID == "" {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	node, _, found := a.findNodeLocked(snapshot.SelectedNodeID)
	if !found || !node.Supported {
		return ""
	}
	return node.ID
}
