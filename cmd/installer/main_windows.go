//go:build windows

// Установщик MS7VPN: один exe, внутри которого лежат сама программа,
// Xray-core, Wintun и деинсталлятор. Ставит всё в папку пользователя,
// создаёт ярлыки и запись в «Установка и удаление программ».
// Права администратора не нужны.
package main

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"ms7vpn/internal/setup"

	"golang.org/x/sys/windows/registry"
)

// Встраиваемые файлы лежат в отдельной папке и собираются скриптом сборки.
// Папка хранится в репозитории с одним пустым файлом: так «go build» проходит
// сразу после клонирования, а сами артефакты (они весят десятки мегабайт)
// в репозиторий не попадают.
//
//go:embed all:payload
var payloadFS embed.FS

func embedded(name string) []byte {
	data, err := payloadFS.ReadFile("payload/" + name)
	if err != nil {
		return nil
	}
	return data
}

const (
	// webview2Bootstrapper — имя файла внутри payload.zip. Если он там есть
	// и WebView2 в системе не установлен, запускаем его перед стартом.
	webview2Bootstrapper = "MicrosoftEdgeWebview2Setup.exe"
)

// staleFiles — то, что оставляли прежние версии установщика. Без этой уборки
// в папке копились лишние десятки мегабайт: второй деинсталлятор и запасная
// копия Xray, которую приложение когда-то скачивало само.
var staleFiles = []string{
	"Uninstall-MS7VPN.exe",
	"xray_no_window.vbs",
	"xray_no_window.ps1",
}

func main() {
	if len(os.Args) > 1 && strings.EqualFold(os.Args[1], "/uninstall") {
		if err := setup.Uninstall(); err != nil {
			setup.Message("Удаление MS7VPN", "Не удалось удалить программу:\n"+err.Error(), true)
			os.Exit(1)
		}
		setup.Message("MS7VPN", "Программа удалена.", false)
		return
	}
	if err := install(); err != nil {
		setup.Message("Установка MS7VPN", "Не удалось установить программу:\n"+err.Error(), true)
		os.Exit(1)
	}
}

func install() error {
	target, err := setup.InstallDir()
	if err != nil {
		return err
	}
	setup.KillRunning()

	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("создание папки: %w", err)
	}
	if err := unpack(target); err != nil {
		return err
	}
	cleanStaleFiles(target)
	cleanDownloadedCore()

	appPath := filepath.Join(target, setup.AppName+".exe")
	uninstaller := filepath.Join(target, "unins000.exe")
	if err := writeUninstaller(uninstaller); err != nil {
		return err
	}

	createShortcut(filepath.Join(setup.StartMenuDir(), setup.AppName+".lnk"), appPath, target)
	createShortcut(filepath.Join(setup.DesktopDir(), setup.AppName+".lnk"), appPath, target)
	registerUninstall(target, appPath, uninstaller)

	// WebView2 — движок, которым рисуется окно программы. Без него приложение
	// молча откатывалось на старый интерфейс. Раньше установщик нёс этот файл
	// внутри, но никогда его не запускал.
	ensureWebView2(target)

	command := exec.Command(appPath)
	command.Dir = target
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = command.Start()
	return nil
}

// writeUninstaller кладёт рядом отдельный маленький деинсталлятор.
func writeUninstaller(path string) error {
	if binary := embedded("uninstaller.exe"); len(binary) > 0 {
		if err := os.WriteFile(path, binary, 0o755); err != nil {
			return fmt.Errorf("запись деинсталлятора: %w", err)
		}
		return nil
	}
	// Запасной путь: отдельного деинсталлятора в сборке нет — используем себя.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o755)
}

func cleanStaleFiles(target string) {
	for _, name := range staleFiles {
		_ = os.Remove(filepath.Join(target, name))
	}
}

// cleanDownloadedCore убирает копию Xray, которую прежние версии скачивали в
// папку данных, хотя точно такой же файл уже лежал рядом с программой.
// На диске это освобождает больше шестидесяти мегабайт.
func cleanDownloadedCore() {
	base, err := os.UserConfigDir() // %APPDATA%
	if err != nil {
		return
	}
	_ = os.RemoveAll(filepath.Join(base, setup.AppName, "core"))
}

func unpack(target string) error {
	payload := embedded("payload.zip")
	if len(payload) == 0 {
		return fmt.Errorf("установщик собран без payload.zip — запустите BUILD_WINDOWS.bat")
	}
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return fmt.Errorf("архив повреждён: %w", err)
	}
	root, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		name := filepath.Clean(file.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		path := filepath.Join(root, name)
		absolute, err := filepath.Abs(path)
		if err != nil || (absolute != root && !strings.HasPrefix(absolute, root+string(os.PathSeparator))) {
			continue
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := extractFile(file, path); err != nil {
			return err
		}
	}
	return nil
}

// extractFile пишет файл потоком.
//
// Прежняя версия выделяла буфер размером UncompressedSize64 и читала в него
// вручную: файл на 34 МБ целиком оказывался в памяти, а при неверном размере
// в заголовке ZIP на диск молча попадал обрезанный exe.
func extractFile(file *zip.File, path string) error {
	source, err := file.Open()
	if err != nil {
		return fmt.Errorf("чтение %s: %w", file.Name, err)
	}
	defer source.Close()

	destination, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("запись %s: %w", file.Name, err)
	}
	written, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		return fmt.Errorf("распаковка %s: %w", file.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("запись %s: %w", file.Name, closeErr)
	}
	if size := file.FileInfo().Size(); size > 0 && written != size {
		return fmt.Errorf("файл %s распакован не полностью: %d из %d байт", file.Name, written, size)
	}
	return nil
}

// webView2Installed проверяет, зарегистрирован ли рантайм WebView2.
func webView2Installed() bool {
	const clientKey = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	paths := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + clientKey},
		{registry.LOCAL_MACHINE, `SOFTWARE\` + clientKey},
		{registry.CURRENT_USER, `Software\` + clientKey},
	}
	for _, item := range paths {
		key, err := registry.OpenKey(item.root, item.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		version, _, readErr := key.GetStringValue("pv")
		key.Close()
		if readErr == nil && version != "" && version != "0.0.0.0" {
			return true
		}
	}
	return false
}

func ensureWebView2(target string) {
	bootstrapper := filepath.Join(target, webview2Bootstrapper)
	if webView2Installed() {
		_ = os.Remove(bootstrapper)
		return
	}
	if _, err := os.Stat(bootstrapper); err != nil {
		return
	}
	_ = setup.HiddenCommand(bootstrapper, "/silent", "/install").Run()
	// Установщик рантайма больше не нужен — не оставляем его на диске.
	_ = os.Remove(bootstrapper)
}

// createShortcut делает ярлык через WScript.Shell в PowerShell:
// это не требует COM-биндингов и работает на любой Windows 10/11.
func createShortcut(linkPath, targetPath, workingDir string) {
	script := fmt.Sprintf(
		`$s=(New-Object -COM WScript.Shell).CreateShortcut(%q); $s.TargetPath=%q; $s.WorkingDirectory=%q; $s.IconLocation=%q; $s.Description='MS7 VPN'; $s.Save()`,
		linkPath, targetPath, workingDir, targetPath)
	_ = setup.HiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func registerUninstall(target, appPath, uninstaller string) {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, setup.RegistryKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	_ = key.SetStringValue("DisplayName", "MS7 VPN")
	_ = key.SetStringValue("DisplayVersion", setup.AppVersion)
	_ = key.SetStringValue("Publisher", setup.Publisher)
	_ = key.SetStringValue("DisplayIcon", appPath)
	_ = key.SetStringValue("InstallLocation", target)
	_ = key.SetStringValue("UninstallString", fmt.Sprintf("%q /uninstall", uninstaller))
	_ = key.SetDWordValue("NoModify", 1)
	_ = key.SetDWordValue("NoRepair", 1)
	if size, err := directorySizeKB(target); err == nil {
		_ = key.SetDWordValue("EstimatedSize", size)
	}
}

func directorySizeKB(root string) (uint32, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return uint32(total / 1024), nil
}
