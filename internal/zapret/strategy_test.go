package zapret

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bundlePath — папка с файлами режима в исходниках.
func bundlePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("не удалось определить путь к тесту")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "resources", "zapret")
}

// Файл стратегий из бандла должен проходить проверку целиком.
//
// Тест нужен именно такой: проверка отбрасывает стратегии с незнакомыми
// ключами молча, и если очередной импорт из Flowseal привезёт новый ключ,
// стратегия просто исчезнет из списка. Без этого теста это заметили бы только
// по жалобе «у меня в окне стало меньше стратегий».
func TestBundleStrategiesPassValidation(t *testing.T) {
	path := filepath.Join(bundlePath(t), "strategies.json")
	list, err := LoadStrategies(path)
	if err != nil {
		t.Fatalf("стратегии из бандла не прочитаны: %v", err)
	}
	if len(list) < 20 {
		t.Fatalf("стратегий всего %d — часть отброшена проверкой", len(list))
	}
	for _, strategy := range list {
		if _, rejected := ValidateArgs(strategy.Args); len(rejected) > 0 {
			t.Errorf("стратегия %s: отброшены параметры %v", strategy.ID, rejected)
		}
	}
	if _, ok := FindStrategy(list, "general"); !ok {
		t.Error("в бандле нет стратегии general — её берут по умолчанию")
	}
}

// Автосписок должен быть почти везде.
//
// Почти: у ALT-5 последний профиль вообще без списка доменов, он лечит все
// соединения на своих портах подряд. Автосписок там не нужен — добавлять его
// значило бы делать вид, что профиль ограничен, когда он не ограничен.
func TestBundleStrategiesHaveAutoList(t *testing.T) {
	list, err := LoadStrategies(filepath.Join(bundlePath(t), "strategies.json"))
	if err != nil {
		t.Fatalf("стратегии не прочитаны: %v", err)
	}
	without := 0
	for _, strategy := range list {
		if !strings.Contains(strings.Join(strategy.Args, " "), placeholderAuto) {
			without++
			t.Logf("без автосписка: %s", strategy.ID)
		}
	}
	if without > 1 {
		t.Errorf("стратегий без автосписка %d, ожидалась не больше одной", without)
	}
}

func TestValidateArgsRejectsForeignFlags(t *testing.T) {
	cases := []struct {
		name string
		arg  string
	}{
		{"глобальный ключ программы", "--daemon"},
		{"запись файла по своему пути", "--wf-save=C:\\windows\\system32\\drivers\\x.sys"},
		{"выход вместо работы", "--dry-run"},
		{"выдуманный ключ", "--eat-all-traffic"},
		{"не длинный ключ", "-x"},
		{"кавычка в значении", `--filter-tcp=80"`},
		{"путь без плейсхолдера", "--hostlist=C:\\temp\\my.txt"},
		{"сетевой путь", `--ipset-exclude=\\attacker\share\list.txt`},
	}
	for _, test := range cases {
		clean, rejected := ValidateArgs([]string{test.arg})
		if len(rejected) != 1 || len(clean) != 0 {
			t.Errorf("%s: %q принят", test.name, test.arg)
		}
	}
}

func TestValidateArgsKeepsDesyncFlags(t *testing.T) {
	args := []string{
		"--new", "--filter-tcp=443", "--dpi-desync=fake,multisplit",
		"--dpi-desync-split-seqovl-pattern={fake}/tls_clienthello_www_google_com.bin",
		"--hostlist-auto={auto}", "--ip-id=zero",
	}
	clean, rejected := ValidateArgs(args)
	if len(rejected) > 0 {
		t.Fatalf("отброшены нужные параметры: %v", rejected)
	}
	if len(clean) != len(args) {
		t.Fatalf("принято %d из %d", len(clean), len(args))
	}
}

func TestExpandPlaceholders(t *testing.T) {
	args := Expand([]string{
		"--hostlist={lists}/list-general.txt",
		"--dpi-desync-fake-quic={fake}/quic.bin",
		"--hostlist-auto={auto}",
		"--dpi-desync=fake",
	}, `C:\app\fake`, `C:\app\lists`, `C:\data\autohostlist.txt`)

	want := []string{
		`--hostlist=C:\app\lists\list-general.txt`,
		`--dpi-desync-fake-quic=C:\app\fake\quic.bin`,
		`--hostlist-auto=C:\data\autohostlist.txt`,
		"--dpi-desync=fake",
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("аргумент %d: получили %q, ожидали %q", i, args[i], want[i])
		}
	}
}

// Путь не должен получать приставку \\?\: winws собран под cygwin и такие
// пути не открывает — падает с «cannot access ipset file».
func TestExpandKeepsPlainPaths(t *testing.T) {
	for _, arg := range Expand([]string{"--hostlist={lists}/a.txt"}, "f", `C:\app\lists`, "a") {
		if strings.Contains(arg, `\\?\`) {
			t.Fatalf("в аргументе приставка расширенного пути: %q", arg)
		}
	}
}

func TestParseStrategiesDropsDuplicatesAndEmpty(t *testing.T) {
	data := []byte(`{"strategies":[
		{"id":"a","name":"A","args":["--dpi-desync=fake"]},
		{"id":"a","name":"Дубль","args":["--dpi-desync=fake"]},
		{"id":"","name":"Без имени","args":["--dpi-desync=fake"]},
		{"id":"b","name":"B","args":[]},
		{"id":"c","name":"C","args":["--daemon"]},
		{"id":"d","args":["--dpi-desync=fake"]}
	]}`)
	list, err := ParseStrategies(data)
	if err != nil {
		t.Fatalf("разбор не удался: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("осталось %d стратегий, ожидали 2: %+v", len(list), list)
	}
	if list[1].Name != "d" {
		t.Errorf("стратегии без названия должно достаться её имя, получили %q", list[1].Name)
	}
}

func TestParseStrategiesFailsOnEmptyFile(t *testing.T) {
	if _, err := ParseStrategies([]byte(`{"strategies":[]}`)); err == nil {
		t.Error("пустой файл стратегий принят")
	}
	if _, err := ParseStrategies([]byte(`{`)); err == nil {
		t.Error("битый файл стратегий принят")
	}
}
