package singbox

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ms7vpn/internal/xray"
)

const Version = "1.14.4"
const DownloadURL = "https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-windows-amd64.zip"
const ArchiveSHA256 = "99185d75d314a07dbba4a130c8ad986cbeb629a07927975636b341ca61f991df"

type Manager struct {
	mu        sync.Mutex
	path, dir string
	cmd       *exec.Cmd
	exited    chan struct{}
	progress  func(xray.Progress)
}

func NewManager(dataDir string, progress func(xray.Progress)) *Manager {
	path := filepath.Join(dataDir, "core", "sing-box-"+Version, "sing-box.exe")
	if exe, err := os.Executable(); err == nil {
		for _, dir := range []string{filepath.Dir(exe), filepath.Join(filepath.Dir(exe), "core")} {
			candidate := filepath.Join(dir, "sing-box.exe")
			if info, err := os.Stat(candidate); err == nil && info.Size() > 1000000 {
				path = candidate
				break
			}
		}
	}
	return &Manager{path: path, dir: filepath.Join(dataDir, "runtime", "sing-box"), progress: progress}
}
func (m *Manager) CorePath() string { return m.path }
func (m *Manager) Installed() bool {
	i, e := os.Stat(m.path)
	return e == nil && !i.IsDir() && i.Size() > 1000000
}
func (m *Manager) Ensure(ctx context.Context) error {
	if m.Installed() {
		return nil
	}
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return err
	}
	archive := filepath.Join(m.dir, "sing-box-download.zip")
	notify := func(p int) {
		if m.progress != nil {
			m.progress(xray.Progress{Downloading: true, Percent: p, Message: fmt.Sprintf("Загрузка sing-box: %d%%", p)})
		}
	}
	if err := xray.DownloadVerified(ctx, DownloadURL, archive, ArchiveSHA256, notify); err != nil {
		return err
	}
	defer os.Remove(archive)
	if err := ExtractBinary(archive, m.path); err != nil {
		return err
	}
	if m.progress != nil {
		m.progress(xray.Progress{Percent: 100, Message: "sing-box установлен"})
	}
	return nil
}

// Extract only the expected executable. Paths from an archive never become
// output paths, and the verified zip cannot overwrite application files.
func ExtractBinary(archive, destination string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, f := range reader.File {
		if filepath.Base(f.Name) != "sing-box.exe" || f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > 150<<20 {
			return errors.New("слишком большой sing-box.exe")
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		temp, err := os.CreateTemp(filepath.Dir(destination), "sing-box-*.tmp")
		if err != nil {
			return err
		}
		defer os.Remove(temp.Name())
		_, copyErr := io.Copy(temp, io.LimitReader(in, 150<<20))
		closeErr := temp.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Rename(temp.Name(), destination)
	}
	return errors.New("официальный архив не содержит sing-box.exe")
}

func (m *Manager) Start(ctx context.Context, config []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil {
		return errors.New("sing-box уже запущен")
	}
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(m.dir, "config.json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		return err
	}
	check := exec.CommandContext(ctx, m.path, "check", "-c", path)
	check.Dir = filepath.Dir(m.path)
	xray.PrepareChild(check)
	if output, err := check.CombinedOutput(); err != nil {
		return fmt.Errorf("sing-box отклонил конфигурацию: %s", strings.TrimSpace(string(output)))
	}
	log, err := os.OpenFile(filepath.Join(m.dir, "sing-box.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	cmd := exec.Command(m.path, "run", "-c", path)
	cmd.Dir = filepath.Dir(m.path)
	cmd.Stdout = log
	cmd.Stderr = log
	xray.PrepareChild(cmd)
	if err := cmd.Start(); err != nil {
		log.Close()
		return err
	}
	xray.AdoptChild(cmd)
	done := make(chan struct{})
	m.cmd = cmd
	m.exited = done
	go func() {
		_ = cmd.Wait()
		_ = log.Close()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
		}
		close(done)
		m.mu.Unlock()
	}()
	return nil
}
func (m *Manager) WaitAlive(ctx context.Context) error {
	m.mu.Lock()
	done := m.exited
	m.mu.Unlock()
	if done == nil {
		return errors.New("sing-box не запущен")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return fmt.Errorf("sing-box завершился: %s", m.TailLog(15))
	case <-time.After(700 * time.Millisecond):
		return nil
	}
}
func (m *Manager) Running() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.cmd != nil }
func (m *Manager) Stop() error {
	m.mu.Lock()
	cmd, done := m.cmd, m.exited
	m.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("sing-box не завершился за 5 секунд")
	}
}
func (m *Manager) TailLog(n int) string {
	b, err := os.ReadFile(filepath.Join(m.dir, "sing-box.log"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

type StatsClient struct {
	port     int
	secret   string
	client   *http.Client
	mu       sync.Mutex
	at       time.Time
	up, down int64
}

func NewStatsClient(port int, secret string) *StatsClient {
	return &StatsClient{port: port, secret: secret, client: &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}}
}
func (s *StatsClient) Query(ctx context.Context) (xray.Traffic, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/connections", s.port), nil)
	if err != nil {
		return xray.Traffic{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.secret)
	r, err := s.client.Do(req)
	if err != nil {
		return xray.Traffic{}, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return xray.Traffic{}, fmt.Errorf("sing-box API: HTTP %d", r.StatusCode)
	}
	var totals struct {
		Up   int64 `json:"uploadTotal"`
		Down int64 `json:"downloadTotal"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&totals); err != nil {
		return xray.Traffic{}, err
	}
	result := xray.Traffic{UplinkBytes: totals.Up, DownlinkBytes: totals.Down}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() {
		dt := now.Sub(s.at).Seconds()
		if dt >= .2 && totals.Up >= s.up && totals.Down >= s.down {
			result.UplinkBitsSec = int64(float64(totals.Up-s.up) * 8 / dt)
			result.DownlinkBitsSec = int64(float64(totals.Down-s.down) * 8 / dt)
		}
	}
	s.at = now
	s.up = totals.Up
	s.down = totals.Down
	return result, nil
}
func (s *StatsClient) Reset() { s.mu.Lock(); s.at = time.Time{}; s.up = 0; s.down = 0; s.mu.Unlock() }
