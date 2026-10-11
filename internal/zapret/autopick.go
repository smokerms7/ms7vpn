package zapret

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Progress — ход подбора для окна.
//
// Отдаётся вместе с состоянием приложения: окно опрашивает его и так, а
// отдельный канал событий пришлось бы держать открытым всё время подбора.
type Progress struct {
	Running   bool   `json:"running"`
	Index     int    `json:"index"`
	Total     int    `json:"total"`
	Current   string `json:"current,omitempty"`
	CurrentID string `json:"currentId,omitempty"`
	Done      bool   `json:"done"`
	Found     string `json:"found,omitempty"`
	FoundID   string `json:"foundId,omitempty"`
	Error     string `json:"error,omitempty"`

	// StepError — почему сорвался отдельный шаг. В окне не показывается:
	// одна неудачная стратегия подбор не останавливает. Но в журнал попасть
	// должна, иначе «не запустилась» и «не помогла» выглядят одинаково.
	StepError string `json:"-"`

	// Full отмечает, что найденная стратегия открыла и YouTube, и Discord.
	// Главный экран по этому признаку решает, закрывать окно подбора и
	// включаться сразу или оставить человека выбирать, что ему важнее.
	Full bool `json:"full"`
}

// settleDelay — пауза между запуском winws и проверкой.
//
// Драйвер начинает перехватывать не мгновенно, и без паузы первый запрос
// успевал уйти мимо обхода: стратегия выглядела нерабочей через раз.
const settleDelay = 900 * time.Millisecond

// AutoPick перебирает стратегии и останавливается на первой, при которой
// открылись обе группы сайтов.
//
// Перебор идёт с реально поднятым обходом — иначе проверка ничего не значит.
// Поэтому он медленный: на каждую стратегию уходит запуск winws, проверка и
// остановка. Быстрее проверить нельзя.
//
// Если полностью рабочей не нашлось, возвращается лучшая из частичных: пусть
// откроется YouTube без Discord — это всё равно лучше, чем ничего, а что
// важнее, решает человек в окне выбора.
func (m *Manager) AutoPick(ctx context.Context, onStep func(Progress)) (Strategy, Probe, error) {
	if onStep == nil {
		onStep = func(Progress) {}
	}
	list, err := m.Strategies()
	if err != nil {
		return Strategy{}, Probe{}, err
	}
	if err := HasInternet(ctx); err != nil {
		return Strategy{}, Probe{}, err
	}

	// Остатки прошлого запуска исказили бы первую же проверку.
	_ = m.Stop()

	var (
		best      Strategy
		bestProbe Probe
		bestScore int
	)
	for index, strategy := range list {
		if err := ctx.Err(); err != nil {
			return best, bestProbe, err
		}
		onStep(Progress{Running: true, Index: index + 1, Total: len(list),
			Current: strategy.Name, CurrentID: strategy.ID})

		if err := m.Start(ctx, strategy); err != nil {
			// Неудачный запуск — это не приговор всему подбору: одна
			// стратегия может ссылаться на файл, которого нет в бандле.
			m.RememberResult(strategy.ID, GroupResult{})
			onStep(Progress{Running: true, Index: index + 1, Total: len(list),
				Current: strategy.Name, CurrentID: strategy.ID,
				StepError: strategy.ID + ": " + err.Error()})
			continue
		}
		select {
		case <-ctx.Done():
			_ = m.Stop()
			return best, bestProbe, ctx.Err()
		case <-time.After(settleDelay):
		}

		probe := CheckBoth(ctx)
		_ = m.Stop()
		if ctx.Err() != nil {
			return best, bestProbe, ctx.Err()
		}
		m.RememberResult(strategy.ID, GroupResult{YouTube: probe.YouTube, Discord: probe.Discord})

		points := 0
		if probe.YouTube {
			points += 2
		}
		if probe.Discord {
			points += 2
		}
		if points > bestScore {
			best, bestProbe, bestScore = strategy, probe, points
		}
		if probe.YouTube && probe.Discord {
			m.MarkPicked()
			onStep(Progress{Index: index + 1, Total: len(list), Done: true,
				Found: strategy.Name, FoundID: strategy.ID, Full: true})
			return strategy, probe, nil
		}
	}

	m.MarkPicked()
	if bestScore == 0 {
		err := errors.New("ни одна стратегия не помогла — похоже, провайдер блокирует по IP, включите VPN")
		onStep(Progress{Index: len(list), Total: len(list), Done: true, Error: err.Error()})
		return Strategy{}, Probe{}, err
	}
	onStep(Progress{Index: len(list), Total: len(list), Done: true,
		Found: best.Name, FoundID: best.ID})
	return best, bestProbe, nil
}

// CheckCurrent проверяет, помогает ли уже включённый обход.
func (m *Manager) CheckCurrent(ctx context.Context) (Probe, error) {
	if !m.Running() {
		return Probe{}, fmt.Errorf("обход не включён")
	}
	return CheckBoth(ctx), nil
}
