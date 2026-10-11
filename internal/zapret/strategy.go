// Package zapret — режим «Запрет»: обход DPI без своего сервера.
//
// Своего кода обхода здесь нет. Пакет запускает готовый winws из проекта
// zapret (bol-van) дочерним процессом. winws через драйвер WinDivert
// перехватывает исходящие пакеты к заблокированным сайтам и портит начало
// соединения так, что оборудование провайдера не узнаёт сайт, а настоящий
// сервер всё понимает.
//
// Отсюда свойства режима: трафик идёт напрямую, пинг родной, подписка не
// тратится. И отсюда ограничения: только Windows, только с правами
// администратора, и сайты, заблокированные по IP, он не открывает — для них
// нужен обычный туннель.
package zapret

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Strategy — именованный набор аргументов winws.
//
// Стратегии не универсальны: что обходит ТСПУ у одного провайдера, у другого
// не делает ничего. Поэтому они лежат данными в strategies.json, а не в коде,
// и обновляются вместе со списками, без пересборки программы.
type Strategy struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Args []string `json:"args"`
}

// StrategyFile — формат strategies.json.
//
// source хранит коммиты первоисточников, из которых собраны стратегии и
// списки: по ним видно, насколько они устарели.
type StrategyFile struct {
	Source     map[string]string `json:"source,omitempty"`
	Strategies []Strategy        `json:"strategies"`
}

// Плейсхолдеры в аргументах. Пути зависят от машины, поэтому в файле их нет:
// клиент подставляет их перед запуском.
const (
	placeholderFake  = "{fake}"  // папка с поддельными пакетами
	placeholderLists = "{lists}" // папка с готовыми списками доменов
	placeholderAuto  = "{auto}"  // файл автосписка, winws пополняет его сам
)

// allowedFlags — ключи winws, которые разрешено брать из стратегии.
//
// Это не формальность. Стратегия — аргументы процесса, который поднимает
// драйвер уровня ядра, а файл со стратегиями лежит на диске рядом с
// программой и когда-нибудь будет приезжать с сервера. Поэтому принимаются
// только известные ключи, а всё прочее отбрасывается с объяснением.
//
// Сюда намеренно не входят ключи, меняющие поведение самого процесса, а не
// обхода: они перечислены в deniedFlags.
//
// Ключи семейства --dpi-desync* разрешены по приставке: их десятки, они
// профильные и все задают только способ порчи первых пакетов. Список сверен
// с winws из бандла zapret-win-bundle (v72.12).
var allowedFlags = map[string]bool{
	"--new": true, "--filter-tcp": true, "--filter-udp": true,
	"--filter-l3": true, "--filter-l7": true,

	// Что перехватывать на уровне драйвера.
	"--wf-tcp": true, "--wf-udp": true, "--wf-l3": true, "--wf-iface": true,
	"--wf-raw": true,

	// Списки доменов и адресов.
	"--hostlist": true, "--hostlist-domains": true,
	"--hostlist-exclude": true, "--hostlist-exclude-domains": true,
	"--hostlist-auto": true, "--hostlist-auto-fail-threshold": true,
	"--hostlist-auto-fail-time": true, "--hostlist-auto-retrans-threshold": true,
	"--ipset": true, "--ipset-ip": true, "--ipset-exclude": true, "--ipset-exclude-ip": true,

	// Мелочи, которыми стратегии пользуются наравне с desync: порча
	// заголовков, размер окна, дубли и правка исходного пакета. Все они
	// профильные и сверены с `shade-winws --help` выпуска v72.12 — выдуманных
	// ключей тут быть не должно: winws выходит с кодом 1 на первом
	// незнакомом, и стратегия с опечаткой не запустилась бы вовсе.
	"--ip-id": true, "--methodeol": true, "--hostcase": true,
	"--hostnospace": true, "--domcase": true, "--hostspell": true,
	"--wsize": true, "--wssize": true, "--wssize-cutoff": true,
	"--wssize-forced-cutoff": true, "--synack-split": true,
	"--skip": true, "--comment": true,
	"--wf-filter-lan": true, "--wf-raw-part": true,
}

// Ключи семейств --dpi-desync*, --dup* и --orig* разрешены по приставке.
//
// Их около шестидесяти, они профильные, и каждый задаёт только способ порчи
// пакетов. Перечислять их поимённо значило бы терять стратегии при каждом
// обновлении winws: незнакомый ключ отбрасывается, а вместе с ним молча
// исчезает вся стратегия.
var allowedPrefixes = []string{"--dpi-desync", "--dup", "--orig"}

// deniedFlags — ключи, которые задаёт программа, а не стратегия.
//
// Держим их отдельно от просто неизвестных, чтобы объяснить причину отказа.
// --daemon и --pidfile меняют способ запуска, из-за чего мы потеряли бы
// процесс и не смогли его остановить. --wf-save и --debug пишут файлы по
// указанному пути. --dry-run и --version заставили бы winws выйти сразу, а
// запуск при этом считался бы удачным.
var deniedFlags = map[string]bool{
	"--daemon": true, "--pidfile": true, "--user": true, "--uid": true,
	"--chdir": true, "--writable": true, "--import": true,
	"--wf-save": true, "--wf-dup-check": true, "--blob": true,
	"--debug": true, "--dry-run": true, "--version": true, "--help": true,
	"--ssid-filter": true, "--nlm-filter": true, "--nlm-list": true,
	"--ctrack-disable": true, "--ctrack-timeouts": true,
	"--ipcache-hostname": true, "--ipcache-lifetime": true,
}

// safeValue — что допускается в значении ключа.
//
// Запрещены кавычки, точка с запятой, амперсанд, вертикальная черта и
// управляющие символы. Аргументы передаются процессу массивом, а не через
// оболочку, так что внедрить команду нельзя и так; проверка — второй рубеж
// на случай, если когда-нибудь кто-то соберёт из них строку.
var safeValue = regexp.MustCompile(`^[^\x00-\x1f"'` + "`" + `;&|<>$]*$`)

// ValidateArgs пропускает только разрешённые ключи.
//
// Возвращает отфильтрованный список и перечень отброшенного — чтобы молча не
// проглатывать непонятное, а показать человеку.
func ValidateArgs(args []string) (clean []string, rejected []string) {
	for _, arg := range args {
		if reason := rejectReason(arg); reason != "" {
			rejected = append(rejected, arg+" ("+reason+")")
			continue
		}
		clean = append(clean, arg)
	}
	return clean, rejected
}

// rejectReason объясняет, почему параметр не принят. Пустая строка — принят.
func rejectReason(arg string) string {
	if !strings.HasPrefix(arg, "--") {
		return "не длинный ключ"
	}
	flag, value, hasValue := strings.Cut(arg, "=")
	if deniedFlags[flag] {
		return "ключ задаёт программа, а не стратегия"
	}
	if !allowedFlags[flag] && !hasAllowedPrefix(flag) {
		return "неизвестный ключ"
	}
	if !hasValue {
		return ""
	}
	if !safeValue.MatchString(value) {
		return "недопустимый символ в значении"
	}
	// Путь в значении разрешён только через плейсхолдер. Иначе стратегия
	// могла бы указать winws любой файл на диске, в том числе сетевой.
	if strings.ContainsAny(value, `/\`) && !hasPlaceholderPrefix(value) {
		return "путь задан не через {fake}, {lists} или {auto}"
	}
	return ""
}

func hasAllowedPrefix(flag string) bool {
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(flag, prefix) {
			return true
		}
	}
	return false
}

func hasPlaceholderPrefix(value string) bool {
	return strings.HasPrefix(value, placeholderFake) ||
		strings.HasPrefix(value, placeholderLists) ||
		strings.HasPrefix(value, placeholderAuto)
}

// Expand подставляет в аргументы настоящие пути.
//
// Пути передаются обычными (C:\...), без приставки \\?\: winws собран под
// cygwin и такие пути не открывает — падает на «cannot access ipset file».
func Expand(args []string, fakeDir, listsDir, autoFile string) []string {
	out := make([]string, 0, len(args))
	replacer := strings.NewReplacer(
		placeholderFake, fakeDir,
		placeholderLists, listsDir,
		placeholderAuto, autoFile,
	)
	for _, arg := range args {
		arg = replacer.Replace(arg)
		// Разделители внутри значения приводим к родным для системы: в файле
		// они записаны косой чертой, а cygwin понимает оба вида, но в
		// сообщениях об ошибках смешанные пути читаются плохо.
		if flag, value, ok := strings.Cut(arg, "="); ok && strings.ContainsAny(value, `/\`) {
			arg = flag + "=" + filepath.FromSlash(value)
		}
		out = append(out, arg)
	}
	return out
}

// ParseStrategies разбирает содержимое strategies.json и отбрасывает негодное.
func ParseStrategies(data []byte) ([]Strategy, error) {
	var file StrategyFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("файл стратегий не разобран: %w", err)
	}
	var out []Strategy
	seen := make(map[string]bool, len(file.Strategies))
	for _, strategy := range file.Strategies {
		strategy.ID = strings.TrimSpace(strategy.ID)
		if strategy.ID == "" || len(strategy.Args) == 0 || seen[strategy.ID] {
			continue
		}
		clean, rejected := ValidateArgs(strategy.Args)
		if len(rejected) > 0 || len(clean) == 0 {
			continue
		}
		if strings.TrimSpace(strategy.Name) == "" {
			strategy.Name = strategy.ID
		}
		strategy.Args = clean
		seen[strategy.ID] = true
		out = append(out, strategy)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("в файле нет ни одной пригодной стратегии")
	}
	return out, nil
}

// LoadStrategies читает файл стратегий с диска.
func LoadStrategies(path string) ([]Strategy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseStrategies(data)
}

// FindStrategy ищет стратегию по идентификатору.
func FindStrategy(list []Strategy, id string) (Strategy, bool) {
	for _, strategy := range list {
		if strategy.ID == id {
			return strategy, true
		}
	}
	return Strategy{}, false
}
