//go:build windows

package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue     = "MS7VPN"
	AutostartArg = "--autostart"
)

func autostartCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`"%s" %s`, exe, AutostartArg), nil
}

// AutostartEnabled — стоит ли запуск этой копии программы вместе с Windows.
func AutostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(runValue)
	if err != nil {
		return false
	}
	want, err := autostartCommand()
	return err == nil && strings.EqualFold(strings.TrimSpace(value), want)
}

// SetAutostart включает или выключает запуск вместе с Windows (для текущего
// пользователя, без прав администратора).
func SetAutostart(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		if err := key.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	command, err := autostartCommand()
	if err != nil {
		return err
	}
	return key.SetStringValue(runValue, command)
}

// RegisterURLScheme регистрирует ms7vpn:// для текущего пользователя.
// Вызывается при каждом запуске: так ссылка ведёт на установленную сейчас
// копию, даже если программу переставили в другую папку.
func RegisterURLScheme() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	base := `Software\Classes\` + URLScheme
	key, _, err := registry.CreateKey(registry.CURRENT_USER, base, registry.SET_VALUE)
	if err != nil {
		return err
	}
	_ = key.SetStringValue("", "URL:MS7VPN")
	_ = key.SetStringValue("URL Protocol", "")
	key.Close()
	if icon, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\DefaultIcon`, registry.SET_VALUE); err == nil {
		_ = icon.SetStringValue("", fmt.Sprintf(`"%s",0`, exe))
		icon.Close()
	}
	command, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer command.Close()
	return command.SetStringValue("", fmt.Sprintf(`"%s" "%%1"`, exe))
}

// Системные и служебные процессы, которые в списке «Без VPN» только мешают.
var hiddenPrograms = map[string]bool{
	"system": true, "registry": true, "smss.exe": true, "csrss.exe": true, "wininit.exe": true, "winlogon.exe": true,
	"services.exe": true, "lsass.exe": true, "svchost.exe": true, "fontdrvhost.exe": true, "dwm.exe": true,
	"conhost.exe": true, "sihost.exe": true, "taskhostw.exe": true, "ctfmon.exe": true, "runtimebroker.exe": true,
	"searchhost.exe": true, "startmenuexperiencehost.exe": true, "shellexperiencehost.exe": true, "textinputhost.exe": true,
	"dllhost.exe": true, "wmiprvse.exe": true, "spoolsv.exe": true, "audiodg.exe": true, "msmpeng.exe": true,
	"securityhealthservice.exe": true, "securityhealthsystray.exe": true, "smartscreen.exe": true, "memory compression": true,
	"searchindexer.exe": true, "lsaiso.exe": true, "msedgewebview2.exe": true, "ms7vpn.exe": true, "xray.exe": true,
	"sing-box.exe": true, "winws.exe": true, "system idle process": true, "[system process]": true, "cmd.exe": true,
	"powershell.exe": true, "applicationframehost.exe": true, "useroobebroker.exe": true, "nissrv.exe": true,
	"wudfhost.exe": true, "unsecapp.exe": true, "dashost.exe": true, "lockapp.exe": true, "backgroundtaskhost.exe": true,
}

// runningPrograms — имена запущенных программ без системных, по алфавиту.
func runningPrograms() []string {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return []string{}
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	seen := map[string]bool{}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if name == "" || hiddenPrograms[name] || !strings.HasSuffix(name, ".exe") {
			continue
		}
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
