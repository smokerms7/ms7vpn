//go:build windows

package zapret

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// session — запущенный winws и всё, что нужно для остановки.
//
// done закрывается наблюдателем, когда процесс окончательно завершился и
// журнал закрыт. Ждать процесса имеет право только наблюдатель: Wait нельзя
// звать дважды.
type session struct {
	command *exec.Cmd
	started time.Time
	done    chan struct{}
	exitErr error // заполняется до закрытия done
}

// readyMarker — строка, которой winws сообщает, что драйвер поднялся и
// перехват пошёл. Приходит примерно через 0,2 с.
const readyMarker = "windivert initialized. capture is started."

// startTimeout — сколько ждём эту строку.
//
// На холодной загрузке драйвера Windows проверяет подпись и может задуматься
// на несколько секунд, поэтому потолок с запасом. Дольше ждать нет смысла:
// значит, драйвер не поднимется вовсе.
const startTimeout = 15 * time.Second

// stopTimeout — сколько ждём завершения убитого процесса, прежде чем всё
// равно снять службу драйвера.
const stopTimeout = 5 * time.Second

// Start поднимает winws с аргументами стратегии.
func (m *Manager) Start(ctx context.Context, strategy Strategy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running != nil {
		return errors.New("«Запрет» уже включён")
	}
	if m.binDir == "" {
		return errors.New("файлы режима «Запрет» не найдены рядом с программой. Переустановите MS7VPN")
	}
	if !complete(m.binDir) {
		return errors.New("файлы режима «Запрет» повреждены или удалены антивирусом. Переустановите MS7VPN")
	}
	// Права проверяем до запуска, а не после падения winws: без них драйвер
	// отвечает кодом 5, и человеку это надо объяснить словами.
	if !IsAdministrator() {
		return errors.New("нужны права администратора: драйвер перехвата без них не поднимается")
	}
	// Два перехватчика на одном WinDivert портят пакеты друг другу, и
	// выглядит это как «обход сломался». Чужую программу мы не трогаем —
	// говорим о ней и не запускаемся.
	if foreign := foreignBypass(); foreign != "" {
		return fmt.Errorf("уже работает другой обход DPI (%s). Закройте его и попробуйте снова", foreign)
	}

	clean, rejected := ValidateArgs(strategy.Args)
	if len(rejected) > 0 {
		return fmt.Errorf("в стратегии недопустимые параметры: %s", strings.Join(rejected, " "))
	}
	if len(clean) == 0 {
		return errors.New("стратегия пустая")
	}

	// Остатки своего прошлого запуска: если программу убили, winws мог
	// остаться жив и держит драйвер.
	killOwnLeftovers()

	// Файл автосписка должен существовать и быть доступен на запись: winws
	// дописывает в него домены, которые сам распознал как блокируемые.
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	autoPath := m.AutoListPath()
	if file, err := os.OpenFile(autoPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		_ = file.Close()
	} else {
		return fmt.Errorf("не удалось подготовить автосписок: %w", err)
	}

	args := Expand(clean, m.fakeDir(), m.listsDir(), autoPath)

	// Предполётная проверка: winws сам разбирает параметры и выходит, не
	// трогая драйвер. Негодную стратегию ловим здесь и показываем его
	// собственное объяснение — «no such file», «bad value» — вместо кода
	// выхода после попытки поднять ядро.
	if err := m.dryRun(ctx, args); err != nil {
		return err
	}

	logPath := m.LogPath()
	trimLog(logPath)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	stamp := time.Now().Format("2006-01-02 15:04:05")
	_, _ = fmt.Fprintf(logFile, "\n=== %s  стратегия %s (%s) ===\n", stamp, strategy.Name, strategy.ID)

	command := exec.Command(m.ExePath(), args...)
	// Рабочая папка важна: winws собран под cygwin и ищет cygwin1.dll рядом
	// с собой.
	command.Dir = m.binDir
	// Своего ввода у winws нет, а унаследованный дескриптор мешал бы ему
	// завершиться.
	command.Stdin = nil
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = logFile.Close()
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = logFile.Close()
		return err
	}
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("запуск обхода: %w", err)
	}
	// Процесс попадает в job object, который Windows закрывает вместе с
	// нашим. Без этого упавший клиент оставил бы winws жить, и тот продолжил
	// бы трогать трафик, когда его уже никто не ждёт.
	adopt(command)

	ready := make(chan struct{})
	var readyOnce sync.Once
	var tailMu sync.Mutex
	tail := make([]string, 0, 8)

	// Читаем оба потока построчно: winws пишет и в stdout, и в stderr, а
	// признак успеха приходит именно строкой. Буферизации на pipe нет —
	// проверено, строка доходит сразу.
	var readers sync.WaitGroup
	scan := func(source io.Reader) {
		defer readers.Done()
		scanner := bufio.NewScanner(source)
		scanner.Buffer(make([]byte, 0, 64<<10), 256<<10)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = fmt.Fprintln(logFile, line)
			tailMu.Lock()
			tail = append(tail, line)
			if len(tail) > 8 {
				tail = tail[1:]
			}
			tailMu.Unlock()
			if strings.Contains(strings.ToLower(line), readyMarker) {
				readyOnce.Do(func() { close(ready) })
			}
		}
	}
	readers.Add(2)
	go scan(stdout)
	go scan(stderr)

	current := &session{command: command, started: time.Now(), done: make(chan struct{})}
	// Ставим сразу, пока держим замок: наблюдатель ниже тоже захочет его
	// взять и подождёт, пока Start договорит.
	m.running = current
	m.current = strategy
	m.lastExit = ""

	exited := make(chan struct{})
	go func() {
		// Сначала дочитываем вывод: Wait закрывает pipe, и последние строки
		// — те самые, в которых написана причина отказа, — пропали бы.
		readers.Wait()
		current.exitErr = command.Wait()
		_ = logFile.Close()
		close(exited)
		close(current.done)

		m.mu.Lock()
		// Если нас здесь ещё ждут, значит процесс ушёл сам, а не по кнопке:
		// Stop снимает m.running до того, как убить процесс.
		if m.running == current {
			m.running = nil
			tailMu.Lock()
			reason := explainOutput(tail)
			tailMu.Unlock()
			if reason == "" {
				reason = explainExit(current.exitErr)
			}
			m.lastExit = "«Запрет» выключился: " + reason
		}
		m.mu.Unlock()
	}()

	timer := time.NewTimer(startTimeout)
	defer timer.Stop()
	select {
	case <-ready:
		return nil
	case <-exited:
		m.running = nil
		m.current = Strategy{}
		tailMu.Lock()
		reason := explainOutput(tail)
		tailMu.Unlock()
		if reason == "" {
			reason = explainExit(current.exitErr)
		}
		return fmt.Errorf("«Запрет» не запустился: %s", reason)
	case <-ctx.Done():
		m.stopLocked(current)
		return ctx.Err()
	case <-timer.C:
		m.stopLocked(current)
		return errors.New("драйвер перехвата не поднялся за 15 секунд. Проверьте, не блокирует ли его антивирус")
	}
}

// Stop останавливает обход и убирает за собой драйвер.
//
// Снять процесс мало: WinDivert остаётся зарегистрированным службой и держит
// в памяти драйвер уровня ядра, а вместе с ним — файл WinDivert64.sys.
// Из-за этого потом ломаются обновление и удаление программы.
func (m *Manager) Stop() error {
	m.mu.Lock()
	current := m.running
	m.running = nil
	m.current = Strategy{}
	// Выключили кнопкой — объяснять нечего. Иначе причина прошлого падения
	// осталась бы висеть в окне после следующей остановки.
	m.lastExit = ""
	m.mu.Unlock()
	return stopSession(current)
}

// stopLocked останавливает сеанс, когда замок уже у нас.
func (m *Manager) stopLocked(current *session) {
	if m.running == current {
		m.running = nil
		m.current = Strategy{}
	}
	go func() { _ = stopSession(current) }()
}

func stopSession(current *session) error {
	var firstErr error
	if current != nil && current.command.Process != nil {
		if err := current.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			firstErr = err
		}
		// Ждём наблюдателя, а не зовём Wait сами: второй Wait на том же
		// процессе возвращает мусор и портит учёт дескриптора.
		select {
		case <-current.done:
		case <-time.After(stopTimeout):
			if firstErr == nil {
				firstErr = errors.New("процесс обхода не завершился вовремя")
			}
		}
	}
	if err := stopDriverService(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Running сообщает, поднят ли обход.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running != nil
}

// Uptime — сколько обход работает.
func (m *Manager) Uptime() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == nil {
		return 0
	}
	return time.Since(m.running.started)
}

// StartedAt — когда включили обход.
func (m *Manager) StartedAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == nil {
		return time.Time{}
	}
	return m.running.started
}

// killOwnLeftovers снимает наш winws, оставшийся от прошлого сеанса.
//
// Бьём по своему имени файла: под именем winws.exe мы убили бы обход,
// запущенный самим пользователем из Flowseal или GoodbyeDPI.
func killOwnLeftovers() {
	command := exec.Command("taskkill", "/F", "/IM", ExeName)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	_ = command.Run()
}

// otherBypass — чужие обходы, с которыми мы не можем работать одновременно.
var otherBypass = []string{"winws.exe", "winws2.exe", "goodbyedpi.exe"}

// foreignBypass возвращает имя чужого запущенного обхода или пустую строку.
func foreignBypass() string {
	command := exec.Command("tasklist", "/FO", "CSV", "/NH")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	output, err := command.Output()
	if err != nil {
		// Не смогли посмотреть — не повод отказывать в запуске.
		return ""
	}
	lower := strings.ToLower(string(output))
	for _, name := range otherBypass {
		if strings.Contains(lower, `"`+name+`"`) {
			return name
		}
	}
	return ""
}

// stopDriverService снимает службу WinDivert.
//
// Имя зависит от версии драйвера, поэтому пробуем оба известных. Отсутствие
// службы — не ошибка: её могло и не быть, если драйвер не успел подняться.
func stopDriverService() error {
	var lastErr error
	for _, name := range []string{"WinDivert", "WinDivert14"} {
		command := exec.Command("sc", "stop", name)
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		output, err := command.CombinedOutput()
		if err == nil {
			continue
		}
		text := string(output)
		// 1060 — службы нет, 1062 — не запущена. И то и другое нас устраивает.
		if strings.Contains(text, "1060") || strings.Contains(text, "1062") {
			continue
		}
		lastErr = fmt.Errorf("не удалось остановить службу %s: %s", name, strings.TrimSpace(text))
	}
	return lastErr
}

// Cleanup вызывается при выходе из приложения.
//
// Отдельно от Stop: процесса может и не быть, а служба драйвера — остаться
// от аварийно закрытого прошлого сеанса.
func (m *Manager) Cleanup() {
	_ = m.Stop()
	killOwnLeftovers()
	_ = stopDriverService()
}

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
)

// adopt привязывает winws к job object с флагом «убить вместе с заданием».
func adopt(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	jobOnce.Do(func() {
		handle, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		if _, err := windows.SetInformationJobObject(handle,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			_ = windows.CloseHandle(handle)
			return
		}
		jobHandle = handle
	})
	if jobHandle == 0 {
		return
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(command.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(process)
	_ = windows.AssignProcessToJobObject(jobHandle, process)
}

// explainExit переводит код выхода winws на человеческий.
//
// Разбираем код, а не текст: поиск подстроки «5» находил её в «1275» и выдавал
// «запустите от администратора» там, где Windows не пустила драйвер.
func explainExit(err error) string {
	if err == nil {
		return "процесс завершился сам, ничего не написав"
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err.Error()
	}
	switch code := exitErr.ExitCode(); code {
	case 1:
		return "winws отказался от параметров стратегии. Попробуйте другую стратегию или «Подобрать»"
	case 5:
		return "отказано в доступе (код 5). Запустите MS7VPN от имени администратора"
	case 577, 1275:
		return fmt.Sprintf("Windows не пустила драйвер перехвата (код %d). Может мешать Secure Boot или антивирус", code)
	case 1058:
		return "служба драйвера отключена (код 1058). Включите её или переустановите MS7VPN"
	default:
		return fmt.Sprintf("процесс обхода завершился с кодом %d", code)
	}
}

// explainOutput объясняет отказ по тому, что winws написал напоследок.
//
// Типовые причины у режима всего три, и все три человек может устранить сам —
// поэтому их стоит назвать прямо, а не показывать английскую строку из
// журнала.
func explainOutput(tail []string) string {
	text := strings.ToLower(strings.Join(tail, " "))
	switch {
	case strings.Contains(text, "signature"), strings.Contains(text, "blocked"):
		return "Windows или антивирус не дали загрузить драйвер WinDivert. " +
			"Добавьте папку программы в исключения антивируса"
	case strings.Contains(text, "access is denied"), strings.Contains(text, "отказано в доступе"):
		return "отказано в доступе: запустите MS7VPN от имени администратора"
	case strings.Contains(text, "cannot find"), strings.Contains(text, "no such file"):
		return "файл драйвера удалён антивирусом. Переустановите MS7VPN и добавьте его папку в исключения"
	case strings.Contains(text, "cannot access"), strings.Contains(text, "failed to register"):
		return "winws не открыл свои файлы списков. Переустановите MS7VPN"
	}
	// Своих слов нет — отдаём последние строки как есть, они на английском,
	// но хоть что-то для пересылки в поддержку.
	for i := len(tail) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(tail[i]); line != "" {
			return line
		}
	}
	return ""
}

// dryRunTimeout — проверка только разбирает параметры, секунд ей хватает с
// избытком. Потолок нужен, чтобы зависший winws не запер окно навсегда.
const dryRunTimeout = 20 * time.Second

// dryRun просит winws проверить параметры и выйти.
//
// Ключ есть в самом winws: «--dry-run: verify parameters and exit with code 0
// if successful». Драйвер при этом не поднимается, права администратора не
// нужны — поэтому проверку можно гонять и в тестах на всех стратегиях разом.
func (m *Manager) dryRun(ctx context.Context, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, dryRunTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, m.ExePath(), append([]string{"--dry-run"}, args...)...)
	command.Dir = m.binDir
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return errors.New("проверка параметров стратегии не ответила вовремя")
	}
	// Своё объяснение winws пишет последними строками, перед ними — дамп
	// профилей, который человеку здесь не нужен.
	if reason := tailOf(string(output), 3); reason != "" {
		return fmt.Errorf("стратегия не годится: %s", reason)
	}
	return fmt.Errorf("стратегия не годится: %s", explainExit(err))
}

// tailOf возвращает последние непустые строки вывода без баннера версии.
func tailOf(text string, count int) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "github version") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "; ")
}
