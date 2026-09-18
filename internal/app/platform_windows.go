//go:build windows

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type registryValue struct {
	Exists bool   `json:"exists"`
	Type   string `json:"type,omitempty"`
	Value  string `json:"value,omitempty"`
}

type proxyBackup struct {
	ProxyEnable   registryValue `json:"proxyEnable"`
	ProxyServer   registryValue `json:"proxyServer"`
	ProxyOverride registryValue `json:"proxyOverride"`
	AutoConfigURL registryValue `json:"autoConfigUrl"`
}

func proxyBackupPath(dataDir string) string { return filepath.Join(dataDir, "proxy-backup.json") }

func IsAdministrator() bool {
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("IsUserAnAdmin")
	result, _, _ := proc.Call()
	return result != 0
}

func RelaunchElevated(nodeID string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	arguments := "--elevated"
	if nodeID != "" {
		arguments += " --autoconnect=" + quoteArg(nodeID)
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(executable)
	params, _ := syscall.UTF16PtrFromString(arguments)
	directory, _ := syscall.UTF16PtrFromString(filepath.Dir(executable))
	result, _, callErr := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(directory)),
		1,
	)
	if result <= 32 {
		return fmt.Errorf("UAC отклонил запуск: код %d (%v)", result, callErr)
	}
	return nil
}

func OpenExternalURL(rawURL string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(rawURL)
	result, _, callErr := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0, 0, 1,
	)
	if result <= 32 {
		return fmt.Errorf("не удалось открыть ссылку: код %d (%v)", result, callErr)
	}
	return nil
}

func OpenFolder(path string) error {
	command := exec.Command("explorer.exe", path)
	hideCommand(command)
	return command.Start()
}

// openInternetSettings открывает ветку настроек прокси текущего пользователя.
// Раньше каждое чтение и запись выполнялись запуском reg.exe: на одно
// подключение приходилось больше восьми созданий процесса, а с антивирусом
// каждое из них стоит сотни миллисекунд. Прямая работа с реестром — микросекунды.
func openInternetSettings(access uint32) (registry.Key, error) {
	return registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, access)
}

func EnableSystemProxy(dataDir string, httpPort, socksPort int) error {
	backupPath := proxyBackupPath(dataDir)
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		backup := proxyBackup{
			ProxyEnable:   queryRegistryValue("ProxyEnable"),
			ProxyServer:   queryRegistryValue("ProxyServer"),
			ProxyOverride: queryRegistryValue("ProxyOverride"),
			AutoConfigURL: queryRegistryValue("AutoConfigURL"),
		}
		content, _ := json.MarshalIndent(backup, "", "  ")
		if err := os.WriteFile(backupPath, content, 0o600); err != nil {
			return fmt.Errorf("не удалось сохранить настройки proxy: %w", err)
		}
	}
	server := fmt.Sprintf("http=127.0.0.1:%d;https=127.0.0.1:%d;socks=127.0.0.1:%d", httpPort, httpPort, socksPort)
	if err := setRegistryValue("ProxyEnable", "REG_DWORD", "1"); err != nil {
		return err
	}
	if err := setRegistryValue("ProxyServer", "REG_SZ", server); err != nil {
		return err
	}
	if err := setRegistryValue("ProxyOverride", "REG_SZ", `<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.2*;172.30.*;172.31.*;192.168.*`); err != nil {
		return err
	}
	_ = deleteRegistryValue("AutoConfigURL")
	notifyInternetSettings()
	return nil
}

func RestoreSystemProxy(dataDir string) error {
	path := proxyBackupPath(dataDir)
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var backup proxyBackup
	if err := json.Unmarshal(content, &backup); err != nil {
		// Резервная копия испорчена — безопаснее всего просто выключить прокси,
		// иначе пользователь останется без интернета.
		_ = setRegistryValue("ProxyEnable", "REG_DWORD", "0")
		_ = deleteRegistryValue("ProxyServer")
		notifyInternetSettings()
		_ = os.Remove(path)
		return nil
	}
	values := []struct {
		name  string
		value registryValue
	}{
		{"ProxyEnable", backup.ProxyEnable},
		{"ProxyServer", backup.ProxyServer},
		{"ProxyOverride", backup.ProxyOverride},
		{"AutoConfigURL", backup.AutoConfigURL},
	}
	var firstErr error
	for _, item := range values {
		if item.value.Exists {
			if err := setRegistryValue(item.name, item.value.Type, item.value.Value); err != nil && firstErr == nil {
				firstErr = err
			}
		} else {
			_ = deleteRegistryValue(item.name)
		}
	}
	notifyInternetSettings()
	if firstErr == nil {
		_ = os.Remove(path)
	}
	return firstErr
}

// RecoverSystemProxy вызывается при старте приложения: если прошлый сеанс
// завершился аварийно с включённым прокси, система осталась бы без интернета.
func RecoverSystemProxy(dataDir string) error {
	return RestoreSystemProxy(dataDir)
}

// SystemProxyActive сообщает, указывает ли системный прокси на нас.
// Используется сторожем: если пользователь или другая программа сбросили
// настройку, состояние в интерфейсе не должно врать.
func SystemProxyActive(httpPort int) bool {
	key, err := openInternetSettings(registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return false
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil {
		return false
	}
	return strings.Contains(server, fmt.Sprintf("127.0.0.1:%d", httpPort))
}

func queryRegistryValue(name string) registryValue {
	key, err := openInternetSettings(registry.QUERY_VALUE)
	if err != nil {
		return registryValue{}
	}
	defer key.Close()

	if value, _, err := key.GetStringValue(name); err == nil {
		return registryValue{Exists: true, Type: "REG_SZ", Value: value}
	}
	if value, _, err := key.GetIntegerValue(name); err == nil {
		return registryValue{Exists: true, Type: "REG_DWORD", Value: strconv.FormatUint(value, 10)}
	}
	return registryValue{}
}

func setRegistryValue(name, typeName, value string) error {
	key, err := openInternetSettings(registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("доступ к настройкам прокси: %w", err)
	}
	defer key.Close()

	if strings.EqualFold(typeName, "REG_DWORD") {
		number, parseErr := parseRegistryNumber(value)
		if parseErr != nil {
			return parseErr
		}
		if err := key.SetDWordValue(name, number); err != nil {
			return fmt.Errorf("запись %s: %w", name, err)
		}
		return nil
	}
	if err := key.SetStringValue(name, value); err != nil {
		return fmt.Errorf("запись %s: %w", name, err)
	}
	return nil
}

// parseRegistryNumber понимает и десятичный вид, и «0x1» из старых резервных
// копий, сделанных прежней версией через reg.exe.
func parseRegistryNumber(value string) (uint32, error) {
	value = strings.TrimSpace(value)
	base := 10
	if rest, ok := strings.CutPrefix(strings.ToLower(value), "0x"); ok {
		value, base = rest, 16
	}
	number, err := strconv.ParseUint(value, base, 32)
	if err != nil {
		return 0, fmt.Errorf("некорректное числовое значение реестра %q", value)
	}
	return uint32(number), nil
}

func deleteRegistryValue(name string) error {
	key, err := openInternetSettings(registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.DeleteValue(name)
}

func notifyInternetSettings() {
	proc := syscall.NewLazyDLL("wininet.dll").NewProc("InternetSetOptionW")
	const (
		internetOptionRefresh         = 37
		internetOptionSettingsChanged = 39
	)
	_, _, _ = proc.Call(0, internetOptionSettingsChanged, 0, 0)
	_, _, _ = proc.Call(0, internetOptionRefresh, 0, 0)
}

func hideCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// quoteArg экранирует аргумент по правилам разбора командной строки Windows.
// Прежняя версия использовала strconv.Quote — это правила Go, и на значении
// с кавычкой или обратным слэшем команда собиралась неверно.
func quoteArg(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\v\"") {
		return value
	}
	var builder strings.Builder
	builder.WriteByte('"')
	slashes := 0
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			slashes++
		case '"':
			builder.WriteString(strings.Repeat(`\`, slashes*2+1))
			slashes = 0
			builder.WriteByte('"')
			continue
		default:
			slashes = 0
		}
		if value[i] != '"' {
			builder.WriteByte(value[i])
		}
	}
	builder.WriteString(strings.Repeat(`\`, slashes))
	builder.WriteByte('"')
	return builder.String()
}
