package xray

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	CoreVersion  = "v26.3.27"
	CoreURL      = "https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-windows-64.zip"
	CoreSHA256   = "d004c39288ce9ada487c6f398c7c545f7d749e44bdfdd59dbc9f865afba4e1ad"
	WintunURL    = "https://www.wintun.net/builds/wintun-0.14.1.zip"
	WintunSHA256 = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"

	// maxProcessLogBytes — потолок для xray-process.log и access.log.
	// Раньше access.log рос без ограничений и у пользователя дошёл до 4 МБ.
	maxProcessLogBytes = 1 << 20
)

type Progress struct {
	Downloading bool
	Percent     int
	Message     string
	Err         error
}

type Manager struct {
	mu       sync.Mutex
	dataDir  string
	coreDir  string
	runDir   string
	cmd      *exec.Cmd
	logFile  *os.File
	exited   chan struct{}
	onUpdate func(Progress)
}

func NewManager(dataDir string, onUpdate func(Progress)) *Manager {
	coreDir := filepath.Join(dataDir, "core", strings.TrimPrefix(CoreVersion, "v"))
	// Если Xray и Wintun лежат рядом с MS7VPN.exe (так ставит установщик),
	// берём их оттуда и ничего не скачиваем.
	if bundled, ok := bundledCoreDir(); ok {
		coreDir = bundled
	}
	return &Manager{
		dataDir:  dataDir,
		coreDir:  coreDir,
		runDir:   filepath.Join(dataDir, "runtime"),
		onUpdate: onUpdate,
	}
}

// bundledCoreDir ищет xray.exe рядом с исполняемым файлом приложения
// и в подпапке core.
func bundledCoreDir() (string, bool) {
	executable, err := os.Executable()
	if err != nil {
		return "", false
	}
	base := filepath.Dir(executable)
	for _, candidate := range []string{base, filepath.Join(base, "core")} {
		info, err := os.Stat(filepath.Join(candidate, executableName()))
		if err == nil && !info.IsDir() && info.Size() > 1_000_000 {
			return candidate, true
		}
	}
	return "", false
}

func (m *Manager) CorePath() string   { return filepath.Join(m.coreDir, executableName()) }
func (m *Manager) CoreDir() string    { return m.coreDir }
func (m *Manager) RuntimeDir() string { return m.runDir }

func (m *Manager) Installed() bool {
	info, err := os.Stat(m.CorePath())
	return err == nil && !info.IsDir() && info.Size() > 1_000_000
}

func (m *Manager) HasWintun() bool {
	info, err := os.Stat(filepath.Join(m.coreDir, "wintun.dll"))
	return err == nil && !info.IsDir() && info.Size() > 10_000
}

func (m *Manager) notify(progress Progress) {
	if m.onUpdate != nil {
		m.onUpdate(progress)
	}
}

func (m *Manager) Ensure(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Installed() {
		return nil
	}
	if runtime.GOOS != "windows" {
		return errors.New("автоматическая установка Xray этой сборки предназначена для Windows")
	}
	if err := os.MkdirAll(m.coreDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(m.runDir, 0o700); err != nil {
		return err
	}

	m.notify(Progress{Downloading: true, Percent: 0, Message: "Скачивание официального Xray-core…"})
	archivePath := filepath.Join(m.runDir, "xray-download.zip")
	if err := downloadVerified(ctx, CoreURL, archivePath, CoreSHA256, func(percent int) {
		m.notify(Progress{Downloading: true, Percent: percent, Message: fmt.Sprintf("Скачивание Xray-core: %d%%", percent)})
	}); err != nil {
		m.notify(Progress{Err: err, Message: err.Error()})
		return err
	}

	m.notify(Progress{Downloading: true, Percent: 96, Message: "Распаковка Xray-core…"})
	if err := unzipSafe(archivePath, m.coreDir); err != nil {
		m.notify(Progress{Err: err, Message: err.Error()})
		return fmt.Errorf("распаковка Xray: %w", err)
	}
	_ = os.Remove(archivePath)
	if err := normalizeExtractedCore(m.coreDir); err != nil {
		m.notify(Progress{Err: err, Message: err.Error()})
		return err
	}
	if !m.Installed() {
		err := errors.New("после распаковки не найден xray.exe")
		m.notify(Progress{Err: err, Message: err.Error()})
		return err
	}
	if !m.HasWintun() {
		m.notify(Progress{Downloading: true, Percent: 97, Message: "Установка подписанного Wintun…"})
		if err := m.ensureWintun(ctx); err != nil {
			m.notify(Progress{Err: err, Message: err.Error()})
			return err
		}
	}
	m.notify(Progress{Percent: 100, Message: "Xray-core и Wintun установлены"})
	return nil
}

func (m *Manager) ensureWintun(ctx context.Context) error {
	archivePath := filepath.Join(m.runDir, "wintun-download.zip")
	if err := downloadVerified(ctx, WintunURL, archivePath, WintunSHA256, nil); err != nil {
		return fmt.Errorf("скачивание Wintun: %w", err)
	}
	defer os.Remove(archivePath)
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("Wintun zip: %w", err)
	}
	defer r.Close()
	var found *zip.File
	for _, f := range r.File {
		name := strings.ToLower(strings.ReplaceAll(f.Name, "\\", "/"))
		if strings.HasSuffix(name, "/bin/amd64/wintun.dll") || name == "wintun.dll" {
			found = f
			break
		}
	}
	if found == nil {
		return errors.New("в официальном архиве Wintun не найден amd64/wintun.dll")
	}
	in, err := found.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	outPath := filepath.Join(m.coreDir, "wintun.dll")
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !m.HasWintun() {
		return errors.New("wintun.dll распакован некорректно")
	}
	return nil
}

// Start запускает Xray и сразу возвращает управление.
//
// Раньше здесь под мьютексом выполнялись `xray run -test` (чтение geo-файлов,
// секунды) и sleep 900 мс. Всё это время /api/state упирался в тот же мьютекс,
// и окно переставало отвечать. Теперь проверка конфигурации выполняется только
// при неудачном старте — ради понятного текста ошибки.
func (m *Manager) Start(config []byte, mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runningLocked() {
		return errors.New("Xray уже запущен")
	}
	if !m.Installed() {
		return errors.New("Xray-core не установлен")
	}
	if strings.EqualFold(mode, "tun") && !m.HasWintun() {
		return errors.New("рядом с xray.exe не найден wintun.dll")
	}
	if err := os.MkdirAll(m.runDir, 0o700); err != nil {
		return err
	}
	configPath := filepath.Join(m.runDir, "config.json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		return fmt.Errorf("запись конфигурации: %w", err)
	}

	trimLogFile(filepath.Join(m.runDir, "access.log"))
	trimLogFile(filepath.Join(m.runDir, "error.log"))

	logPath := filepath.Join(m.runDir, "xray-process.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	command := exec.Command(m.CorePath(), "run", "-c", configPath)
	command.Dir = m.coreDir
	command.Stdout = logFile
	command.Stderr = logFile
	configureHiddenProcess(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("запуск Xray: %w", err)
	}
	// Процесс попадает в job object, который закрывается вместе с приложением.
	// Без этого упавший MS7VPN оставлял висеть xray.exe с занятым адаптером
	// и портами 10808/10809.
	adoptProcess(command)

	exited := make(chan struct{})
	m.cmd = command
	m.logFile = logFile
	m.exited = exited
	go m.wait(command, logFile, exited)
	return nil
}

// WaitAlive убеждается, что процесс не умер сразу после запуска. Если умер —
// прогоняет `xray run -test`, чтобы вернуть понятную причину вместо
// «завершился сразу после запуска».
func (m *Manager) WaitAlive(ctx context.Context, grace time.Duration) error {
	m.mu.Lock()
	exited := m.exited
	m.mu.Unlock()
	if exited == nil {
		return errors.New("Xray не запущен")
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-exited:
		details := m.TailLog(30)
		if output, err := m.TestConfig(); err != nil {
			return fmt.Errorf("Xray отклонил конфигурацию: %s", firstMeaningfulLine(output))
		}
		return fmt.Errorf("Xray завершился сразу после запуска:\n%s", details)
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// TestConfig прогоняет `xray run -test`. Вызывается только для диагностики,
// когда обычный запуск не удался.
func (m *Manager) TestConfig() (string, error) {
	configPath := filepath.Join(m.runDir, "config.json")
	command := exec.Command(m.CorePath(), "run", "-test", "-c", configPath)
	command.Dir = m.coreDir
	configureHiddenProcess(command)
	output, err := command.CombinedOutput()
	return string(output), err
}

func firstMeaningfulLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Xray ") || strings.HasPrefix(line, "A unified") {
			continue
		}
		return line
	}
	return strings.TrimSpace(output)
}

func (m *Manager) wait(command *exec.Cmd, logFile *os.File, exited chan struct{}) {
	_ = command.Wait()
	_ = logFile.Close()
	close(exited)
	m.mu.Lock()
	if m.cmd == command {
		m.cmd = nil
		m.logFile = nil
		m.exited = nil
	}
	m.mu.Unlock()
}

// Stop останавливает Xray и дожидается, пока процесс действительно исчезнет.
// Раньше функция возвращалась сразу после Kill, и следующий Start успевал
// начаться до того, как освобождались порты и сетевой адаптер.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}
	process := m.cmd.Process
	exited := m.exited
	m.cmd = nil
	m.mu.Unlock()

	if err := terminateProcess(process); err != nil {
		return err
	}
	if exited != nil {
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			return errors.New("Xray не завершился за 5 секунд")
		}
	}
	m.mu.Lock()
	if m.logFile != nil {
		_ = m.logFile.Close()
		m.logFile = nil
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runningLocked()
}

func (m *Manager) runningLocked() bool {
	return m.cmd != nil && m.cmd.Process != nil
}

func (m *Manager) TailLog(lines int) string {
	path := filepath.Join(m.runDir, "xray-process.log")
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	if lines <= 0 {
		lines = 50
	}
	buffer := make([]string, 0, lines)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if len(buffer) == lines {
			copy(buffer, buffer[1:])
			buffer[len(buffer)-1] = scanner.Text()
		} else {
			buffer = append(buffer, scanner.Text())
		}
	}
	return strings.Join(buffer, "\n")
}

// trimLogFile обрезает разросшийся лог, оставляя последнюю половину лимита.
func trimLogFile(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxProcessLogBytes {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	keep := int64(maxProcessLogBytes / 2)
	if _, err := file.Seek(info.Size()-keep, io.SeekStart); err != nil {
		_ = file.Close()
		return
	}
	data, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil {
		return
	}
	// Отрезаем начало до первого перевода строки, чтобы не оставлять огрызок.
	if index := strings.IndexByte(string(data), '\n'); index >= 0 && index+1 < len(data) {
		data = data[index+1:]
	}
	_ = os.WriteFile(path, data, 0o600)
}

func PingTCP(ctx context.Context, address string, port int, timeout time.Duration) (time.Duration, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	endpoint := net.JoinHostPort(address, fmt.Sprint(port))
	dialer := net.Dialer{Timeout: timeout}
	start := time.Now()
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return 0, err
	}
	_ = connection.Close()
	return time.Since(start), nil
}

// WaitLocalPort ждёт, пока Xray откроет локальный порт. Опрос частый и дешёвый:
// это заменяет прежний фиксированный sleep 900 мс.
func WaitLocalPort(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(40 * time.Millisecond):
		}
	}
	return fmt.Errorf("локальный порт %d не открылся", port)
}

// FreeLocalPort подбирает свободный порт, начиная с preferred. Раньше порты
// 10808/10809 были зашиты намертво и конфликтовали с v2rayN или Nekoray,
// запущенными рядом.
func FreeLocalPort(preferred int) int {
	for candidate := preferred; candidate < preferred+40; candidate++ {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(candidate)))
		if err == nil {
			_ = listener.Close()
			return candidate
		}
	}
	return preferred
}

func downloadVerified(ctx context.Context, sourceURL, destination, expectedSHA string, progress func(int)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "MS7VPN core-downloader")
	client := &http.Client{Timeout: 8 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("скачивание: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("скачивание: HTTP %s", response.Status)
	}

	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	hash := sha256.New()
	var written int64
	buffer := make([]byte, 128*1024)
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			if _, err := file.Write(buffer[:read]); err != nil {
				_ = file.Close()
				return err
			}
			_, _ = hash.Write(buffer[:read])
			written += int64(read)
			if progress != nil && response.ContentLength > 0 {
				percent := int(float64(written) / float64(response.ContentLength) * 95)
				if percent > 95 {
					percent = 95
				}
				progress(percent)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = file.Close()
			return readErr
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	actualSHA := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualSHA, expectedSHA) {
		_ = os.Remove(destination)
		return fmt.Errorf("SHA-256 не совпал: получен %s", actualSHA)
	}
	return nil
}

func unzipSafe(source, destination string) error {
	archive, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer archive.Close()
	absoluteDestination, _ := filepath.Abs(destination)
	for _, entry := range archive.File {
		name := filepath.Clean(entry.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("небезопасный путь в ZIP: %s", entry.Name)
		}
		target := filepath.Join(absoluteDestination, name)
		absoluteTarget, _ := filepath.Abs(target)
		if absoluteTarget != absoluteDestination && !strings.HasPrefix(absoluteTarget, absoluteDestination+string(os.PathSeparator)) {
			return fmt.Errorf("выход за каталог при распаковке: %s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if err != nil {
			_ = reader.Close()
			return err
		}
		_, copyErr := io.Copy(output, reader)
		closeErr := output.Close()
		_ = reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func normalizeExtractedCore(root string) error {
	wanted := map[string]string{
		"xray.exe":    filepath.Join(root, "xray.exe"),
		"wintun.dll":  filepath.Join(root, "wintun.dll"),
		"geoip.dat":   filepath.Join(root, "geoip.dat"),
		"geosite.dat": filepath.Join(root, "geosite.dat"),
	}
	found := map[string]string{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		name := strings.ToLower(info.Name())
		if _, ok := wanted[name]; ok {
			if _, already := found[name]; !already {
				found[name] = path
			}
		}
		return nil
	})
	for name, destination := range wanted {
		source, ok := found[name]
		if !ok {
			// geo-файлы нужны только для блокировки рекламы, wintun — только
			// для TUN. Их отсутствие не повод считать установку неудачной.
			if name == "wintun.dll" || name == "geoip.dat" || name == "geosite.dat" {
				continue
			}
			return fmt.Errorf("в архиве Xray отсутствует %s", name)
		}
		if filepath.Clean(source) == filepath.Clean(destination) {
			continue
		}
		if err := copyFile(source, destination); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	closeErr := output.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "xray.exe"
	}
	return "xray"
}

// VerifyHTTPProxy performs a real HTTP request through the local Xray HTTP inbound.
// A connection is considered usable only when an end-to-end request succeeds.
func VerifyHTTPProxy(ctx context.Context, proxyPort int, testURL string) error {
	if testURL == "" {
		testURL = "https://www.gstatic.com/generate_204"
	}
	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
	if err != nil {
		return err
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(proxyURL),
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "MS7VPN connection-check")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 500 {
		return fmt.Errorf("контрольный адрес ответил HTTP %d", resp.StatusCode)
	}
	return nil
}

// VerifySystemPath checks that the OS network path is usable after TUN comes up.
func VerifySystemPath(ctx context.Context, testURL string) error {
	if testURL == "" {
		testURL = "https://www.gstatic.com/generate_204"
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "MS7VPN connection-check")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 500 {
		return fmt.Errorf("контрольный адрес ответил HTTP %d", resp.StatusCode)
	}
	return nil
}
