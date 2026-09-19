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
	AppVersion  = "ms7.vs2.0"
	Publisher   = "MS7 VPN"
	RegistryKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\MS7VPN`
)

// DefaultInstallDir — папка, которую мастер предлагает по умолчанию.
// %LOCALAPPDATA% выбран потому, что запись туда не требует прав администратора.
func DefaultInstallDir() (string, error) {
	base, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Programs", AppName), nil
}

// InstallDir возвращает папку, куда программа установлена на самом деле.
//
// Папку теперь выбирает человек в мастере установки, поэтому вычислять её
// заново нельзя: обновление поставило бы новую версию в %LOCALAPPDATA%, а
// старая осталась бы там, куда её поставили, — на диске оказались бы две
// копии, и ярлыки вели бы на старую. Настоящий путь пишется в реестр при
// установке, отсюда и читаем.
func InstallDir() (string, error) {
	if recorded := RecordedInstallDir(); recorded != "" {
		return recorded, nil
	}
	return DefaultInstallDir()
}

// RecordedInstallDir читает путь установки из реестра.
// Пустая строка означает, что записи нет или папка исчезла.
func RecordedInstallDir() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, RegistryKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetStringValue("InstallLocation")
	if err != nil {
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if info, err := os.Stat(value); err != nil || !info.IsDir() {
		return ""
	}
	return value
}

// DataDir — папка с настройками, подписками и журналом. Обновление её не
// трогает: иначе человек после обновления остался бы без своих подписок.
func DataDir() (string, error) {
	base, err := os.UserConfigDir() // %APPDATA%
	if err != nil {
		return "", err
	}
	return filepath.Join(base, AppName), nil
}

// Writable проверяет, можно ли писать в папку.
//
// Нужна мастеру: если человек выберет Program Files, запись без прав
// администратора не пройдёт, и узнать об этом лучше до начала установки,
// а не на середине распаковки.
func Writable(dir string) bool {
	target := dir
	// Поднимаемся до первой существующей папки: сама папка установки
	// обычно ещё не создана.
	for {
		if info, err := os.Stat(target); err == nil {
			if !info.IsDir() {
				return false
			}
			break
		}
		parent := filepath.Dir(target)
		if parent == target {
			return false
		}
		target = parent
	}
	probe, err := os.CreateTemp(target, ".ms7vpn-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return true
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

// PreviousInstallDirs находит прежние установки, которые лежат не там, куда
// ставим сейчас.
//
// Раньше такие копии оставались на диске навсегда. Установщик чистил только
// ту папку, в которую ставил, а про папку прошлой установки не знал ничего:
// человек один раз поставил программу в другое место, обновился — и получил
// две копии, вторую из которых ничто уже не обновляло.
//
// Проверяем два места: записанное в реестре и предлагаемое по умолчанию.
// Совпадающие с текущей целью и чужие папки отсеиваются.
func PreviousInstallDirs(current string) []string {
	currentKey := dirKey(current)
	seen := map[string]bool{}
	var found []string

	for _, candidate := range []string{RecordedInstallDir(), defaultInstallDirOrEmpty()} {
		key := dirKey(candidate)
		if key == "" || key == currentKey || seen[key] {
			continue
		}
		seen[key] = true
		if !looksLikeOurInstall(candidate) {
			continue
		}
		found = append(found, candidate)
	}
	return found
}

func defaultInstallDirOrEmpty() string {
	dir, err := DefaultInstallDir()
	if err != nil {
		return ""
	}
	return dir
}

func dirKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return strings.ToLower(strings.TrimRight(path, `\/`))
}

// looksLikeOurInstall страхует от удаления чужой папки: сносим её только
// если внутри лежит наш исполняемый файл. Путь в реестре мог остаться от
// давно удалённой программы или быть испорчен вручную.
func looksLikeOurInstall(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, AppName+".exe"))
	return err == nil && !info.IsDir() && info.Size() > 0
}

// RemoveInstallation сносит прежнюю установку целиком.
//
// Данные программы лежат в %APPDATA%\MS7VPN и не затрагиваются: подписки и
// настройки переживают переезд в другую папку.
func RemoveInstallation(dir string) error {
	if !looksLikeOurInstall(dir) {
		return nil
	}
	KillRunning()
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("не удалось удалить прежнюю версию из %s: %w", dir, err)
	}
	// Пустая папка Programs\MS7VPN родителя за собой не тянем: там могут
	// лежать другие программы.
	return nil
}

// PurgeProgramFiles очищает папку программы перед установкой новой версии.
//
// Простая распаковка поверх оставляла файлы, которых в новой сборке уже нет:
// так в папке и накопились лишний деинсталлятор и запасная копия Xray.
// Данные в %APPDATA%\MS7VPN не затрагиваются — там подписки и настройки.
// Файлы из keep не удаляются: это сам работающий установщик.
func PurgeProgramFiles(target string, keep ...string) {
	entries, err := os.ReadDir(target)
	if err != nil {
		return
	}
	protected := make(map[string]bool, len(keep))
	for _, path := range keep {
		if path == "" {
			continue
		}
		if absolute, err := filepath.Abs(path); err == nil {
			protected[strings.ToLower(absolute)] = true
		}
	}
	for _, entry := range entries {
		path := filepath.Join(target, entry.Name())
		if absolute, err := filepath.Abs(path); err == nil && protected[strings.ToLower(absolute)] {
			continue
		}
		_ = os.RemoveAll(path)
	}
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
