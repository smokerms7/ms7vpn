package zapret

import (
	"bytes"
	"io"
	"os"
	"strings"
)

// maxLogBytes — потолок журнала winws. Он пишет много и быстро.
const maxLogBytes = 512 << 10

// trimLog обрезает журнал, оставляя хвост.
//
// Обрезаем, а не удаляем: файл может быть открыт на дозапись, и удалённый
// продолжил бы «писаться» в никуда через уже открытый дескриптор.
func trimLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxLogBytes {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	keep := int64(maxLogBytes / 2)
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
	if index := bytes.IndexByte(data, '\n'); index >= 0 && index+1 < len(data) {
		data = data[index+1:]
	}
	_ = os.WriteFile(path, data, 0o600)
}

// LogTail — хвост журнала winws для страницы «Журнал».
func (m *Manager) LogTail(lines int) string {
	data, err := os.ReadFile(m.LogPath())
	if err != nil {
		return ""
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.TrimSpace(strings.Join(all, "\n"))
}

// ClearLog обрезает журнал winws.
func (m *Manager) ClearLog() error {
	file, err := os.OpenFile(m.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return file.Close()
}
