package zapret

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrefsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	first := NewManager(dir)
	first.RememberResult("alt3", GroupResult{YouTube: true})
	first.MarkPicked()

	second := NewManager(dir)
	prefs := second.Prefs()
	if !prefs.Picked {
		t.Error("отметка о проведённом подборе не сохранилась")
	}
	if result := prefs.Results["alt3"]; !result.YouTube || result.Discord {
		t.Errorf("итог проверки сохранён неверно: %+v", result)
	}
}

// Битый файл настроек не должен ронять режим: подбор занимает минуты, но
// потеря его итогов — это не повод отказываться включаться.
func TestPrefsIgnoreBrokenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zapret.json"), []byte("{не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs := NewManager(dir).Prefs()
	if prefs.StrategyID != "" || prefs.Picked {
		t.Errorf("из битого файла что-то прочиталось: %+v", prefs)
	}
}

// Без бандла режим обязан честно сказать, что он недоступен, а не падать.
func TestManagerWithoutBundle(t *testing.T) {
	m := NewManager(t.TempDir())
	if m.Available() {
		t.Skip("бандл найден рядом с тестом — проверять нечего")
	}
	if _, err := m.Strategies(); err == nil {
		t.Error("без файлов режима стратегии прочитались")
	}
	if m.ExePath() != "" {
		t.Error("без файлов режима выдан путь к winws")
	}
}

// Каталог для окна: выбранная стратегия помечена, проверенные идут раньше
// непроверенных.
func TestCatalogOrdersCheckedFirst(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.binDir = dir
	strategies := `{"strategies":[
		{"id":"a","name":"A","args":["--dpi-desync=fake"]},
		{"id":"b","name":"B","args":["--dpi-desync=fake"]},
		{"id":"c","name":"C","args":["--dpi-desync=fake"]}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "strategies.json"), []byte(strategies), 0o600); err != nil {
		t.Fatal(err)
	}
	m.RememberResult("c", GroupResult{YouTube: true, Discord: true})
	m.RememberResult("b", GroupResult{YouTube: true})
	if err := m.Select("b"); err != nil {
		t.Fatalf("выбор стратегии: %v", err)
	}

	catalog, err := m.Catalog()
	if err != nil {
		t.Fatalf("каталог: %v", err)
	}
	if catalog.Strategies[0].ID != "c" || catalog.Strategies[1].ID != "b" {
		t.Errorf("порядок неверный: %s, %s", catalog.Strategies[0].ID, catalog.Strategies[1].ID)
	}
	if catalog.Strategies[2].Checked {
		t.Error("непроверенная стратегия помечена как проверенная")
	}
	for _, info := range catalog.Strategies {
		if info.Selected != (info.ID == "b") {
			t.Errorf("отметка выбора у %s неверна", info.ID)
		}
	}
	if err := m.Select("нет такой"); err == nil {
		t.Error("выбрана несуществующая стратегия")
	}
}

// Выбранная стратегия пропала из каталога после обновления программы —
// берём первую, а не отказываемся включаться.
func TestSelectedFallsBackToFirst(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.binDir = dir
	if err := os.WriteFile(filepath.Join(dir, "strategies.json"),
		[]byte(`{"strategies":[{"id":"general","name":"General","args":["--dpi-desync=fake"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.prefs.StrategyID = "стратегия-из-прошлой-версии"
	strategy, err := m.Selected()
	if err != nil {
		t.Fatalf("выбор: %v", err)
	}
	if strategy.ID != "general" {
		t.Errorf("взяли %q вместо general", strategy.ID)
	}
}

// Что считается «открылось».
//
// Не код ответа и не объём сам по себе, а дочитанное тело: ТСПУ пропускает
// начало соединения и замораживает его потом. Короткий, но целый ответ —
// это успех: gateway.discord.gg в обычной жизни отвечает пустым 404, а
// hqdefault.jpg весит 16 КБ, и порог в 24 КБ забраковал бы их оба.
func TestFetchEnoughAcceptsCompleteAnswers(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, minProbeBytes+4096))
	}))
	defer big.Close()
	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1500))
	}))
	defer small.Close()
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	client := directClient()
	for name, server := range map[string]*httptest.Server{
		"большой ответ": big, "короткий ответ": small, "пустой 404": notFound,
	} {
		if err := fetchEnough(context.Background(), client, server.URL); err != nil {
			t.Errorf("%s не принят: %v", name, err)
		}
	}
	if err := fetchEnough(context.Background(), client, broken.URL); err == nil {
		t.Error("ответ 500 принят за рабочий")
	}
}

// Оборванное на середине соединение — главный признак того, что стратегия не
// сработала: сервер обещал тело, а оно не пришло.
func TestFetchEnoughRejectsCutConnection(t *testing.T) {
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write(make([]byte, 1024))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Обработчик уходит, тело остаётся недописанным — клиент увидит
		// неожиданный конец потока, как при вмешательстве DPI.
	}))
	defer cut.Close()

	err := fetchEnough(context.Background(), directClient(), cut.URL)
	if err == nil {
		t.Fatal("оборванный ответ принят за рабочий")
	}
	if !strings.Contains(err.Error(), "оборвалось") {
		t.Errorf("непонятное объяснение: %v", err)
	}
}

// Группа обрывается на первой неудаче: иначе подбор двадцати двух стратегий
// ждал бы таймаут на каждом адресе.
func TestCheckGroupFailsFast(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, minProbeBytes))
	}))
	defer good.Close()

	client := directClient()
	if err := checkGroup(context.Background(), client, []string{good.URL, good.URL}); err != nil {
		t.Errorf("рабочая группа не прошла: %v", err)
	}
	// Закрытый порт отвечает отказом сразу.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := closed.URL
	closed.Close()
	if err := checkGroup(context.Background(), client, []string{good.URL, address}); err == nil {
		t.Error("группа с недоступным адресом признана открытой")
	}
}

// Проверка не должна ходить через системный прокси: иначе её итог был бы про
// туннель, а не про обход.
func TestDirectClientIgnoresProxy(t *testing.T) {
	transport, ok := directClient().Transport.(*http.Transport)
	if !ok {
		t.Fatal("тип транспорта изменился")
	}
	if transport.Proxy != nil {
		t.Error("клиент проверки ходит через прокси")
	}
}

// Поиск бандла: рядом с программой, в resources и на пару уровней выше —
// это путь из dist при запуске собранного exe из исходников.
func TestFindBundleFrom(t *testing.T) {
	makeBundle := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range required {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("рядом с программой", func(t *testing.T) {
		root := t.TempDir()
		want := filepath.Join(root, "resources", "zapret")
		makeBundle(want)
		if got := findBundleFrom(root); got != want {
			t.Errorf("нашли %q, ожидали %q", got, want)
		}
	})

	t.Run("на два уровня выше", func(t *testing.T) {
		root := t.TempDir()
		want := filepath.Join(root, "resources", "zapret")
		makeBundle(want)
		deep := filepath.Join(root, "dist", "win")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := findBundleFrom(deep); got != want {
			t.Errorf("нашли %q, ожидали %q", got, want)
		}
	})

	// Неполная папка — это чаще всего след антивируса, вырезавшего winws или
	// драйвер. Брать её нельзя: программа отчиталась бы, что режим доступен,
	// и падала бы при каждом включении.
	t.Run("неполная папка не годится", func(t *testing.T) {
		root := t.TempDir()
		partial := filepath.Join(root, "resources", "zapret")
		if err := os.MkdirAll(partial, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(partial, ExeName), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := findBundleFrom(root); got != "" {
			t.Errorf("взяли неполную папку %q", got)
		}
	})

	t.Run("ничего нет", func(t *testing.T) {
		if got := findBundleFrom(t.TempDir()); got != "" {
			t.Errorf("нашли бандл на пустом месте: %q", got)
		}
	})
}
