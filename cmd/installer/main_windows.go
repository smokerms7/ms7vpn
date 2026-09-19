//go:build windows

// Установщик MS7VPN.
//
// Собирается в двух видах из одного кода:
//   - обычный (онлайн) — весит пару мегабайт, файлы программы скачивает
//     с GitHub во время установки;
//   - полный (сборка с тегом offline) — несёт всё внутри и ставит без
//     интернета.
//
// Режимы запуска:
//
//	без аргументов  окно мастера
//	/update         то же окно, но сразу начинает и потом запускает программу
//	/silent         установка без окна
//	/uninstall      удаление
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ms7vpn/internal/installui"
	"ms7vpn/internal/setup"

	"golang.org/x/sys/windows/registry"
)

const (
	// uninstallerName — как деинсталлятор лежит в архиве.

	// после установки. Второе имя привычно по другим программам.
	uninstallerName = "uninstaller.exe"
	uninstallerFile = "unins000.exe"
)

// staleFiles — то, что оставляли прежние версии установщика. Без этой уборки
// в папке копились лишние десятки мегабайт: второй деинсталлятор и запасная
// копия Xray, которую приложение когда-то скачивало само.
// webview2Installers — чем ставить WebView2, в порядке предпочтения.
//
// Автономный установщик (около 130 МБ) лежит только в полной сборке и
// работает без интернета. Маленький загрузчик тянет рантайм из сети сам.
// Если в архиве нет ни того, ни другого, программа откатится на запасной
// интерфейс — окно всё равно откроется.
var webview2Installers = []string{
	"MicrosoftEdgeWebView2RuntimeInstallerX64.exe",
	"MicrosoftEdgeWebview2Setup.exe",
}

var staleFiles = []string{
	"Uninstall-MS7VPN.exe",
	"xray_no_window.vbs",
	"xray_no_window.ps1",
}

func main() {
	switch strings.ToLower(argument()) {
	case "/uninstall":
		if err := setup.Uninstall(); err != nil {
			setup.Message("Удаление MS7VPN", "Не удалось удалить программу:\n"+err.Error(), true)
			os.Exit(1)
		}
		setup.Message("MS7VPN", "Программа удалена.", false)

	case "/silent":
		target := installTarget()
		if _, err := install(target, func(int, string) {}); err != nil {
			setup.Message("Установка MS7VPN", "Не удалось установить программу:\n"+err.Error(), true)
			os.Exit(1)
		}
		launch(target)

	case "/update":
		runWizard(true)

	default:
		runWizard(false)
	}
}

func argument() string {
	if len(os.Args) > 1 {
		return strings.TrimSpace(os.Args[1])
	}
	return ""
}

// installTarget — куда ставить. Если программа уже установлена, берём её
// нынешнюю папку: иначе на диске оказались бы две копии, а ярлыки вели бы
// на старую.
func installTarget() string {
	if existing := setup.RecordedInstallDir(); existing != "" {
		return existing
	}
	target, err := setup.DefaultInstallDir()
	if err != nil {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", setup.AppName)
	}
	return target
}

func runWizard(autoStart bool) {
	existing := setup.RecordedInstallDir()
	upgrade := existing != ""
	target := installTarget()

	caption := "Установка MS7 VPN"
	if upgrade {
		caption = "Обновление MS7 VPN"
	}

	request := installui.Request{
		Caption: caption,
		Product: "MS7 VPN",
		Version: strings.TrimPrefix(setup.AppVersion, "ms7.vs"),
		// Папку выбирают только при первой установке. При обновлении менять
		// её нельзя: старая версия осталась бы на месте вместе с ярлыками.
		AllowChooseDir: !upgrade,
		DefaultDir:     target,
		Hint:           installHint(upgrade),
		AutoStart:      autoStart,
		Upgrade:        upgrade,
		ValidateDir:    validateDir,
		Bullets: []string{
			"Быстрое подключение",
			"Обход блокировок",
			"Без ограничений",
		},
	}

	outcome := installui.Run(request, install)
	if outcome.Err != nil {
		setup.Message("Установка MS7VPN", outcome.Err.Error(), true)
		os.Exit(1)
	}
	if !outcome.Installed {
		return
	}
	// После обновления программу возвращаем сама: человек её не закрывал,
	// это мы её закрыли, чтобы заменить файлы.
	if outcome.Launch || autoStart {
		launch(outcome.Dir)
	}
}

// validateDir объясняет человеку, почему выбранная папка не подходит,
// пока установка ещё не началась.
func validateDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "Укажите папку для установки."
	}
	if !filepath.IsAbs(dir) {
		return "Укажите полный путь, например C:\\Programs\\MS7VPN."
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return "По этому пути уже есть файл с таким именем. Выберите другую папку."
	}
	if !setup.Writable(dir) {
		return "В эту папку нельзя записывать без прав администратора.\n\n" +
			"Выберите другую — например, папку внутри «Локальные данные» " +
			"вашего пользователя. MS7 VPN не требует прав администратора " +
			"для установки."
	}
	return ""
}

// launch запускает установленную программу.
//
// Никакого HideWindow здесь быть не должно. Этот флаг не «прячет консоль»,
// он кладёт SW_HIDE в STARTUPINFO, а Windows передаёт это значение дочернему
// процессу как состояние его ПЕРВОГО окна. WebView2 в таком окне считает,
// что показывать нечего, и не рисует: окно есть, содержимое белое. Отсюда
// и бралось белое окно ровно при первом запуске — том, который делает
// установщик. Запуск с ярлыка работал, потому что там STARTUPINFO обычный.
//
// Прятать всё равно нечего: MS7VPN.exe собран с -H windowsgui и консоли
// не имеет.
func launch(target string) {
	appPath := filepath.Join(target, setup.AppName+".exe")
	command := exec.Command(appPath)
	command.Dir = target
	_ = command.Start()
}

// install выполняет установку целиком. report получает ход работы в процентах.
func install(target string, report installui.Reporter) (string, error) {
	// Папки прежних версий, которые удалить не вышло: файл мог быть занят.
	var leftovers []string

	report(2, "Подготовка")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("создание папки: %w", err)
	}
	setup.KillRunning()

	// Скачиваем (или достаём из себя) до того, как тронем установленную
	// версию: если интернета нет, у человека останется рабочая программа.
	payload, err := obtainPayload(report)
	if err != nil {
		return "", err
	}

	// Прежняя установка в другой папке: сносим её, иначе на диске останутся
	// две копии, и ярлыки будут вести на ту, которую больше никто не обновит.
	for _, previous := range setup.PreviousInstallDirs(target) {
		report(71, "Удаление прежней версии")
		if err := setup.RemoveInstallation(previous); err != nil {
			// Не останавливаем установку: новая версия важнее уборки.
			// Человек увидит предупреждение в конце.
			leftovers = append(leftovers, previous)
		}
	}

	report(72, "Замена файлов")
	self, _ := os.Executable()
	setup.PurgeProgramFiles(target, self)

	report(76, "Распаковка")
	if err := unpack(payload, target, report); err != nil {
		return "", err
	}
	cleanStaleFiles(target)
	cleanDownloadedCore()

	appPath := filepath.Join(target, setup.AppName+".exe")
	if _, err := os.Stat(appPath); err != nil {
		return "", fmt.Errorf("в архиве нет %s.exe — сборка установщика испорчена", setup.AppName)
	}

	report(90, "Ярлыки")
	uninstaller := filepath.Join(target, uninstallerFile)
	if err := ensureUninstaller(uninstaller); err != nil {
		return "", err
	}
	createShortcut(filepath.Join(setup.StartMenuDir(), setup.AppName+".lnk"), appPath, target)
	createShortcut(filepath.Join(setup.DesktopDir(), setup.AppName+".lnk"), appPath, target)
	registerUninstall(target, appPath, uninstaller)

	// WebView2 — движок, которым рисуется окно программы. Без него приложение
	// молча откатывается на запасной интерфейс.
	report(94, "Проверка WebView2")
	ensureWebView2(target)

	report(100, "Готово")
	if len(leftovers) > 0 {
		return "Прежнюю версию удалить не удалось, осталась папка:\n" +
			strings.Join(leftovers, "\n") +
			"\nУдалите её вручную — программа работает и без этого.", nil
	}
	return "", nil
}

// ensureUninstaller проверяет, что деинсталлятор на месте. Он приходит внутри
// архива; если сборка сделана без него, кладём копию самого установщика.
func ensureUninstaller(path string) error {
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return fmt.Errorf("запись деинсталлятора: %w", err)
	}
	return nil
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

func unpack(payload []byte, target string, report installui.Reporter) error {
	if len(payload) == 0 {
		return fmt.Errorf("установщик собран без файлов программы — запустите BUILD_WINDOWS.bat")
	}
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return fmt.Errorf("архив повреждён: %w", err)
	}
	root, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	// Общий объём нужен, чтобы полоса двигалась ровно: xray.exe занимает
	// почти весь архив, и без учёта размеров она стояла бы на месте, а потом
	// прыгала до конца.
	var total, written int64
	for _, file := range reader.File {
		total += file.FileInfo().Size()
	}
	if total == 0 {
		total = 1
	}

	for _, file := range reader.File {
		name := filepath.Clean(file.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		// Деинсталлятор кладём под привычным именем.
		if strings.EqualFold(name, uninstallerName) {
			name = uninstallerFile
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
		written += file.FileInfo().Size()
		report(76+int(14*written/total), "Распаковка")
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
	installed := webView2Installed()
	for _, name := range webview2Installers {
		path := filepath.Join(target, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if !installed {
			_ = setup.HiddenCommand(path, "/silent", "/install").Run()
			installed = webView2Installed()
		}
		// Установщик рантайма весит десятки мегабайт и после установки
		// не нужен — на диске его не оставляем.
		_ = os.Remove(path)
	}
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
