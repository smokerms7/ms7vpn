//go:build windows

// Package setup содержит части, общие для установщика и деинсталлятора.
//
// Раньше деинсталлятором служила полная копия установщика: она несла внутри
// весь payload и занимала на диске больше двадцати мегабайт до самого удаления
// программы. Общий код вынесен сюда, чтобы деинсталлятор был отдельной
// маленькой программой.
package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	AppName     = "MS7VPN"
	AppVersion  = "ms7.vs1.2"
	Publisher   = "MS7 VPN"
	RegistryKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\MS7VPN`
)

func InstallDir() (string, error) {
	base, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Programs", AppName), nil
}

func HiddenCommand(name string, args ...string) *exec.Cmd {
	command := exec.Command(name, args...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return command
}

// KillRunning закрывает запущенную программу и её ядро, иначе файлы заняты.
// Раньше taskkill запускался без скрытия окна и моргал консолью.
func KillRunning() {
	_ = HiddenCommand("taskkill", "/IM", AppName+".exe", "/F").Run()
	_ = HiddenCommand("taskkill", "/IM", "xray.exe", "/F").Run()
}

func StartMenuDir() string {
	appData, _ := os.UserConfigDir() // %APPDATA%
	dir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// DesktopDir находит настоящий рабочий стол. OneDrive-перенаправление
// проверяется первым: когда рабочий стол уехал в облачную папку, локальный
// ~\Desktop остаётся пустой заглушкой, и ярлык в нём пользователь не увидит.
func DesktopDir() string {
	home, _ := os.UserHomeDir()
	for _, candidate := range []string{
		filepath.Join(home, "OneDrive", "Desktop"),
		filepath.Join(home, "OneDrive", "Рабочий стол"),
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "Рабочий стол"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return filepath.Join(home, "Desktop")
}

// Uninstall удаляет программу, ярлыки и запись в списке установленных.
func Uninstall() error {
	target, err := InstallDir()
	if err != nil {
		return err
	}
	KillRunning()
	_ = os.Remove(filepath.Join(StartMenuDir(), AppName+".lnk"))
	_ = os.Remove(filepath.Join(DesktopDir(), AppName+".lnk"))
	_ = registry.DeleteKey(registry.CURRENT_USER, RegistryKey)

	entries, _ := os.ReadDir(target)
	self, _ := os.Executable()
	for _, entry := range entries {
		path := filepath.Join(target, entry.Name())
		if strings.EqualFold(path, self) {
			continue
		}
		_ = os.RemoveAll(path)
	}

	// Свой файл удалить на ходу нельзя, поэтому чистим отложенной командой.
	// rmdir без /s /q не удалял папку, если в ней что-то оставалось.
	script := fmt.Sprintf(`ping 127.0.0.1 -n 3 >nul & del /f /q %s & rmdir /s /q %s`,
		QuoteForCmd(self), QuoteForCmd(target))
	return HiddenCommand("cmd", "/c", script).Start()
}

// QuoteForCmd оборачивает путь в кавычки для cmd /c. Кавычки внутри пути
// невозможны в файловой системе Windows, поэтому просто убираем их.
func QuoteForCmd(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, ``) + `"`
}

func Message(title, text string, isError bool) {
	flags := uint32(windows.MB_OK)
	if isError {
		flags |= windows.MB_ICONERROR
	} else {
		flags |= windows.MB_ICONINFORMATION
	}
	titlePtr, _ := windows.UTF16PtrFromString(title)
	textPtr, _ := windows.UTF16PtrFromString(text)
	user32 := windows.NewLazySystemDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	messageBox.Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), uintptr(flags))
}
