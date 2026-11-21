//go:build windows

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Прокси для консольных программ.
//
// Системный прокси Windows читают браузеры и большинство окон, но не Node.js,
// git, curl, pip и Claude Code — они смотрят только на переменные
// HTTP_PROXY/HTTPS_PROXY. Без них такие программы в режиме «Системный прокси»
// ходят напрямую, и сервисы видят настоящий регион. На время подключения
// прописываем переменные пользователя, прежние значения сохраняем в файл и
// возвращаем при отключении, выходе и при следующем запуске после сбоя.

var envProxyNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}

type envBackup struct {
	Values  map[string]string `json:"values"`
	Present map[string]bool   `json:"present"`
}

func envBackupPath(dataDir string) string { return filepath.Join(dataDir, "env-proxy-backup.json") }

func EnableConsoleProxy(dataDir string, httpPort int) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	// Резервную копию делаем только один раз: повторное подключение не
	// должно затереть настоящие значения нашими же.
	if _, err := os.Stat(envBackupPath(dataDir)); errors.Is(err, os.ErrNotExist) {
		backup := envBackup{Values: map[string]string{}, Present: map[string]bool{}}
		for _, name := range envProxyNames {
			if value, _, err := key.GetStringValue(name); err == nil {
				backup.Values[name] = value
				backup.Present[name] = true
			}
		}
		data, _ := json.Marshal(backup)
		if err := os.WriteFile(envBackupPath(dataDir), data, 0o600); err != nil {
			return err
		}
	}
	proxy := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	for name, value := range map[string]string{
		"HTTP_PROXY":  proxy,
		"HTTPS_PROXY": proxy,
		"NO_PROXY":    ourNoProxy,
	} {
		if err := key.SetStringValue(name, value); err != nil {
			return err
		}
	}
	broadcastEnvironmentChange()
	return nil
}

// RestoreConsoleProxy возвращает переменные как были. Без резервной копии
// ничего не делает: значит, мы их не трогали.
func RestoreConsoleProxy(dataDir string) error {
	data, err := os.ReadFile(envBackupPath(dataDir))
	if err != nil {
		cleanStaleConsoleProxy()
		return nil
	}
	var backup envBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		_ = os.Remove(envBackupPath(dataDir))
		return err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	for _, name := range envProxyNames {
		// Значение на мёртвый локальный порт — хвост прошлого сеанса или
		// остановленного VPN, а не настройка пользователя: не возвращаем.
		if backup.Present[name] && !(name != "NO_PROXY" && deadLoopbackProxy(backup.Values[name])) {
			_ = key.SetStringValue(name, backup.Values[name])
		} else {
			_ = key.DeleteValue(name)
		}
	}
	broadcastEnvironmentChange()
	return os.Remove(envBackupPath(dataDir))
}

// ourNoProxy — значение NO_PROXY, которое ставит только эта программа.
const ourNoProxy = "localhost,127.0.0.1,::1"

// cleanStaleConsoleProxy убирает наши переменные, если резервной копии нет,
// а они остались (программу убили, или копия была снята с уже наших
// значений). Узнаём их по точному NO_PROXY и прокси на мёртвый локальный
// порт — иначе git, npm, curl и проверка обновлений стучатся в пустоту.
func cleanStaleConsoleProxy() {
	key, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	noProxy, _, err := key.GetStringValue("NO_PROXY")
	if err != nil || noProxy != ourNoProxy {
		return
	}
	httpProxy, _, _ := key.GetStringValue("HTTP_PROXY")
	if !deadLoopbackProxy(httpProxy) {
		return
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		_ = key.DeleteValue(name)
	}
	broadcastEnvironmentChange()
}

// broadcastEnvironmentChange сообщает Проводнику и открытым программам, что
// переменные изменились: новые окна терминала их подхватят.
func broadcastEnvironmentChange() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("SendMessageTimeoutW")
	param, _ := windows.UTF16PtrFromString("Environment")
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	var result uintptr
	proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(param)), smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&result)))
}
