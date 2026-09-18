//go:build windows

package tray

import (
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	procShowWindow        = user32.NewProc("ShowWindow")
	procIsWindowVisible   = user32.NewProc("IsWindowVisible")
	procSetWindowLongPtr  = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc    = user32.NewProc("CallWindowProcW")
	procSetForegroundMain = user32.NewProc("SetForegroundWindow")
)

const (
	gwlpWndProc = -4

	swHide    = 0
	swShow    = 5
	swRestore = 9
)

var (
	windowMu     sync.Mutex
	mainWindow   uintptr
	originalProc uintptr
	hideOnClose  func() bool
)

// AttachWindow перехватывает закрытие главного окна.
//
// Если включена настройка «сворачивать в область уведомлений», крестик прячет
// окно вместо выхода. Это важно не только для удобства: раньше закрытие окна
// означало выход, и пользователь, свернувший программу крестиком, терял
// соединение, сам того не желая.
func AttachWindow(handle uintptr, shouldHide func() bool) {
	if handle == 0 {
		return
	}
	windowMu.Lock()
	defer windowMu.Unlock()
	if mainWindow != 0 {
		return
	}
	mainWindow = handle
	hideOnClose = shouldHide
	// GWLP_WNDPROC отрицателен, поэтому приводим через переменную:
	// прямое преобразование отрицательной константы в uintptr запрещено.
	index := int32(gwlpWndProc)
	previous, _, _ := procSetWindowLongPtr.Call(handle, uintptr(index), windows.NewCallback(mainWndProc))
	originalProc = previous
}

func mainWndProc(handle, message, wParam, lParam uintptr) uintptr {
	windowMu.Lock()
	previous := originalProc
	shouldHide := hideOnClose
	windowMu.Unlock()

	if message == wmClose && shouldHide != nil && shouldHide() {
		procShowWindow.Call(handle, uintptr(swHide))
		return 0
	}
	if previous != 0 {
		result, _, _ := procCallWindowProc.Call(previous, handle, message, wParam, lParam)
		return result
	}
	result, _, _ := procDefWindowProc.Call(handle, message, wParam, lParam)
	return result
}

// ShowMainWindow разворачивает окно и выносит его на передний план.
func ShowMainWindow() {
	windowMu.Lock()
	handle := mainWindow
	windowMu.Unlock()
	if handle == 0 {
		return
	}
	visible, _, _ := procIsWindowVisible.Call(handle)
	if visible == 0 {
		procShowWindow.Call(handle, uintptr(swShow))
	}
	procShowWindow.Call(handle, uintptr(swRestore))
	procSetForegroundMain.Call(handle)
}

const notifyIconSettingsPath = `Software\Microsoft\Windows\CurrentVersion\NotifyIconSettings`

// PromoteIcon просит Windows показывать значок прямо на панели задач, а не
// прятать его под стрелку «Отображать скрытые значки».
//
// Запись появляется только после того, как значок был показан хотя бы раз,
// поэтому вызывать функцию имеет смысл уже после создания значка. Windows
// уважает эту настройку не на всех сборках, и неудача здесь не проблема:
// значок в любом случае доступен под стрелкой, откуда его можно вытащить мышью.
func PromoteIcon(executablePath string) {
	if executablePath == "" {
		return
	}
	root, err := registry.OpenKey(registry.CURRENT_USER, notifyIconSettingsPath, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return
	}
	names, err := root.ReadSubKeyNames(-1)
	root.Close()
	if err != nil {
		return
	}
	target := strings.ToLower(filepath.Clean(executablePath))
	for _, name := range names {
		sub, openErr := registry.OpenKey(registry.CURRENT_USER,
			notifyIconSettingsPath+`\`+name, registry.QUERY_VALUE|registry.SET_VALUE)
		if openErr != nil {
			continue
		}
		path, _, readErr := sub.GetStringValue("ExecutablePath")
		if readErr == nil && strings.ToLower(filepath.Clean(path)) == target {
			_ = sub.SetDWordValue("IsPromoted", 1)
		}
		sub.Close()
	}
}
