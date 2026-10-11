package zapret

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Файлы режима лежат рядом с программой, в resources/zapret: это бинарники с
// драйвером, им не место в папке данных, которую пользователь правит руками.
const (
	resourceDir = "resources"
	bundleDir   = "zapret"

	// ExeName — наш winws. Переименован нарочно: зачистка остатков бьёт по
	// имени процесса, и под своим именем она убивала бы winws, запущенный
	// самим пользователем из Flowseal или GoodbyeDPI.
	ExeName = "shade-winws.exe"
)

// required — без этих файлов запускать нечего.
var required = []string{ExeName, "cygwin1.dll", "WinDivert.dll", "WinDivert64.sys"}

// Manager отвечает за файлы режима на диске, за выбранную стратегию и за
// запущенный winws.
type Manager struct {
	dataDir string
	binDir  string

	mu      sync.Mutex
	running *session

	// current — стратегия, с которой поднят обход.
	current Strategy

	// lastExit — почему обход отвалился сам.
	//
	// winws может продержаться несколько секунд и умереть: драйвер сняли,
	// антивирус вмешался. Запуск к тому моменту уже отчитался об успехе, и
	// без этого поля окно погасило бы «Включено» без единого слова о причине.
	lastExit string

	// strategiesMu защищает разобранный файл стратегий.
	strategiesMu sync.Mutex
	strategies   []Strategy

	prefsMu sync.Mutex
	prefs   Prefs
}

// Prefs — что мы помним о подборе между запусками.
//
// Results хранится по идентификатору стратегии: подбор занимает минуты, и
// терять его итоги при перезапуске нельзя. Picked отмечает, что подбор хоть
// раз доходил до конца, — по нему главный экран решает, предлагать его при
// первом включении или просто включаться.
type Prefs struct {
	StrategyID string                 `json:"strategyId,omitempty"`
	Picked     bool                   `json:"picked,omitempty"`
	Results    map[string]GroupResult `json:"results,omitempty"`
}

// GroupResult — чем кончилась проверка стратегии: открылись ли группы сайтов.
type GroupResult struct {
	YouTube bool `json:"youtube"`
	Discord bool `json:"discord"`
}

// NewManager создаёт менеджер. binDir ищется рядом с программой; пустая
// строка означает, что бандл не найден и режим недоступен.
func NewManager(dataDir string) *Manager {
	m := &Manager{dataDir: dataDir, binDir: findBundle(dataDir)}
	m.prefs = m.loadPrefs()
	return m
}

// findBundle ищет папку с бинарниками рядом с программой.
//
// Ищем только относительно самого исполняемого файла и поднимаемся от него
// вверх — это нужно для запуска из исходников, где exe лежит в dist, а файлы
// режима в корне проекта.
//
// Рабочая папка процесса и папка данных сюда намеренно не входят, хотя так
// было бы удобнее. winws запускается с правами администратора: нашёлся бы он
// в произвольной папке, откуда программу запустили, — и достаточно было бы
// положить рядом свой shade-winws.exe и ярлык, чтобы получить выполнение
// кода от администратора. Папка программы такого не даёт: кто может писать в
// неё, может подменить и саму программу.
func findBundle(string) string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return findBundleFrom(filepath.Dir(executable))
}

// findBundleFrom — та же поисковая логика от заданной папки. Вынесена, чтобы
// её можно было проверить тестом: os.Executable в тестах указывает на сам
// тестовый бинарник во временной папке.
func findBundleFrom(root string) string {
	for depth := 0; depth < 3; depth++ {
		for _, candidate := range []string{
			filepath.Join(root, resourceDir, bundleDir),
			filepath.Join(root, bundleDir),
		} {
			if complete(candidate) {
				return candidate
			}
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	return ""
}

// complete сообщает, лежат ли в папке все обязательные файлы.
func complete(dir string) bool {
	for _, name := range required {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.IsDir() || info.Size() == 0 {
			return false
		}
	}
	return true
}

// Available сообщает, есть ли на диске всё нужное для работы режима.
func (m *Manager) Available() bool { return m.binDir != "" && isWindows }

// BinDir — папка с бинарниками. Пустая строка, если бандла нет.
func (m *Manager) BinDir() string { return m.binDir }

// ExePath — путь к нашему winws.
func (m *Manager) ExePath() string {
	if m.binDir == "" {
		return ""
	}
	return filepath.Join(m.binDir, ExeName)
}

func (m *Manager) fakeDir() string  { return filepath.Join(m.binDir, "fake") }
func (m *Manager) listsDir() string { return filepath.Join(m.binDir, "lists") }

// AutoListPath — файл автосписка.
//
// Лежит в папке данных, а не рядом с бинарниками: winws дописывает в него
// найденные домены, то есть файл меняется, а папка программы может быть
// доступна только на чтение (установка в Program Files).
func (m *Manager) AutoListPath() string { return filepath.Join(m.dataDir, "autohostlist.txt") }

// LogPath — журнал winws.
func (m *Manager) LogPath() string { return filepath.Join(m.dataDir, "zapret.log") }

func (m *Manager) prefsPath() string { return filepath.Join(m.dataDir, "zapret.json") }

// StrategiesPath — файл стратегий.
func (m *Manager) StrategiesPath() string {
	if m.binDir == "" {
		return ""
	}
	return filepath.Join(m.binDir, "strategies.json")
}

// Strategies отдаёт разобранный каталог стратегий.
//
// Файл читается один раз и дальше держится в памяти: подбор обращается к
// каталогу на каждом шаге, а строк в файле несколько тысяч.
func (m *Manager) Strategies() ([]Strategy, error) {
	m.strategiesMu.Lock()
	defer m.strategiesMu.Unlock()
	if m.strategies != nil {
		return m.strategies, nil
	}
	path := m.StrategiesPath()
	if path == "" {
		return nil, errors.New("файлы режима «Запрет» не найдены рядом с программой")
	}
	list, err := LoadStrategies(path)
	if err != nil {
		return nil, fmt.Errorf("стратегии не прочитаны: %w", err)
	}
	m.strategies = list
	return list, nil
}

// Selected возвращает выбранную стратегию.
//
// Если сохранённого идентификатора нет в каталоге — например, программа
// обновилась и стратегию переименовали, — берётся первая. Молчаливый откат
// здесь уместнее отказа: человек нажал «Включить», а не «Выбрать».
func (m *Manager) Selected() (Strategy, error) {
	list, err := m.Strategies()
	if err != nil {
		return Strategy{}, err
	}
	id := m.Prefs().StrategyID
	if strategy, ok := FindStrategy(list, id); ok {
		return strategy, nil
	}
	return list[0], nil
}

// Prefs отдаёт копию сохранённых настроек режима.
func (m *Manager) Prefs() Prefs {
	m.prefsMu.Lock()
	defer m.prefsMu.Unlock()
	copied := m.prefs
	copied.Results = make(map[string]GroupResult, len(m.prefs.Results))
	for id, result := range m.prefs.Results {
		copied.Results[id] = result
	}
	return copied
}

// Select запоминает выбранную стратегию.
func (m *Manager) Select(id string) error {
	list, err := m.Strategies()
	if err != nil {
		return err
	}
	if _, ok := FindStrategy(list, id); !ok {
		return fmt.Errorf("стратегия %q не найдена", id)
	}
	m.prefsMu.Lock()
	m.prefs.StrategyID = id
	prefs := m.prefs
	m.prefsMu.Unlock()
	return m.savePrefs(prefs)
}

// RememberResult сохраняет итог проверки одной стратегии.
func (m *Manager) RememberResult(id string, result GroupResult) {
	m.prefsMu.Lock()
	if m.prefs.Results == nil {
		m.prefs.Results = map[string]GroupResult{}
	}
	m.prefs.Results[id] = result
	prefs := m.prefs
	m.prefsMu.Unlock()
	_ = m.savePrefs(prefs)
}

// MarkPicked отмечает, что подбор доходил до конца.
func (m *Manager) MarkPicked() {
	m.prefsMu.Lock()
	m.prefs.Picked = true
	prefs := m.prefs
	m.prefsMu.Unlock()
	_ = m.savePrefs(prefs)
}

func (m *Manager) loadPrefs() Prefs {
	data, err := os.ReadFile(m.prefsPath())
	if err != nil {
		return Prefs{Results: map[string]GroupResult{}}
	}
	var prefs Prefs
	if err := json.Unmarshal(data, &prefs); err != nil {
		return Prefs{Results: map[string]GroupResult{}}
	}
	if prefs.Results == nil {
		prefs.Results = map[string]GroupResult{}
	}
	return prefs
}

// savePrefs пишет настройки через временный файл: прерванная запись не должна
// оставить обрезанный json, из-за которого потерялись бы итоги подбора.
func (m *Manager) savePrefs(prefs Prefs) error {
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.prefsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.prefsPath()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// CurrentStrategy — стратегия, с которой обход поднят сейчас.
func (m *Manager) CurrentStrategy() Strategy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// LastExit — объяснение последнего самопроизвольного выхода.
func (m *Manager) LastExit() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastExit
}

// StrategyList — каталог для окна: стратегии вместе с итогами подбора.
type StrategyList struct {
	Strategies []StrategyInfo `json:"strategies"`
}

// StrategyInfo — одна строка в окне выбора.
type StrategyInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	YouTube  *bool  `json:"youtube,omitempty"`
	Discord  *bool  `json:"discord,omitempty"`
	Selected bool   `json:"selected"`
	Checked  bool   `json:"checked"`
}

// Catalog собирает каталог стратегий с запомненными итогами.
func (m *Manager) Catalog() (StrategyList, error) {
	list, err := m.Strategies()
	if err != nil {
		return StrategyList{}, err
	}
	prefs := m.Prefs()
	selected, _ := m.Selected()
	out := StrategyList{Strategies: make([]StrategyInfo, 0, len(list))}
	for _, strategy := range list {
		info := StrategyInfo{ID: strategy.ID, Name: strategy.Name, Selected: strategy.ID == selected.ID}
		if result, ok := prefs.Results[strategy.ID]; ok {
			youtube, discord := result.YouTube, result.Discord
			info.YouTube, info.Discord, info.Checked = &youtube, &discord, true
		}
		out.Strategies = append(out.Strategies, info)
	}
	// Проверенные наверх, внутри — сначала те, где открылось больше.
	sort.SliceStable(out.Strategies, func(i, j int) bool {
		return score(out.Strategies[i]) > score(out.Strategies[j])
	})
	return out, nil
}

func score(info StrategyInfo) int {
	if !info.Checked {
		return 0
	}
	points := 1
	if info.YouTube != nil && *info.YouTube {
		points += 2
	}
	if info.Discord != nil && *info.Discord {
		points += 2
	}
	return points
}
