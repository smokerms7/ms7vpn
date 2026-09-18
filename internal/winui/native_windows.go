//go:build windows

package winui

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	ms7app "ms7vpn/internal/app"
	"ms7vpn/internal/model"
)

type HWND uintptr
type HBRUSH uintptr
type HFONT uintptr
type HDC uintptr

//go:embed MS7VPN.ico
var embeddedIcon []byte

const (
	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_VISIBLE          = 0x10000000
	WS_CHILD            = 0x40000000
	WS_TABSTOP          = 0x00010000
	WS_VSCROLL          = 0x00200000
	WS_BORDER           = 0x00800000
	WS_CLIPCHILDREN     = 0x02000000

	ES_AUTOHSCROLL = 0x0080
	ES_MULTILINE   = 0x0004
	ES_READONLY    = 0x0800
	ES_AUTOVSCROLL = 0x0040

	BS_OWNERDRAW = 0x0000000B

	LBS_NOTIFY           = 0x0001
	LBS_NOINTEGRALHEIGHT = 0x0100

	CBS_DROPDOWNLIST = 0x0003

	SW_SHOW = 5
	SW_HIDE = 0

	WM_CREATE          = 0x0001
	WM_DESTROY         = 0x0002
	WM_SIZE            = 0x0005
	WM_SETFOCUS        = 0x0007
	WM_SETICON         = 0x0080
	WM_PAINT           = 0x000F
	WM_CLOSE           = 0x0010
	WM_COMMAND         = 0x0111
	WM_TIMER           = 0x0113
	WM_CTLCOLORBTN     = 0x0135
	WM_CTLCOLORSTATIC  = 0x0138
	WM_CTLCOLOREDIT    = 0x0133
	WM_CTLCOLORLISTBOX = 0x0134
	WM_DRAWITEM        = 0x002B
	WM_APP             = 0x8000

	BN_CLICKED    = 0
	LBN_SELCHANGE = 1
	LBN_DBLCLK    = 2
	CBN_SELCHANGE = 1

	LB_ADDSTRING    = 0x0180
	LB_RESETCONTENT = 0x0184
	LB_GETCURSEL    = 0x0188
	LB_SETCURSEL    = 0x0186

	CB_ADDSTRING = 0x0143
	CB_GETCURSEL = 0x0147
	CB_SETCURSEL = 0x014E

	WM_SETFONT = 0x0030

	COLOR_WINDOW = 5
	TRANSPARENT  = 1

	DT_CENTER     = 0x00000001
	DT_VCENTER    = 0x00000004
	DT_SINGLELINE = 0x00000020
	DT_LEFT       = 0x00000000

	MB_OK              = 0x00000000
	MB_ICONERROR       = 0x00000010
	MB_ICONWARNING     = 0x00000030
	MB_ICONINFORMATION = 0x00000040
	MB_YESNO           = 0x00000004
	IDYES              = 6

	SWP_NOZORDER = 0x0004

	CF_UNICODETEXT  = 13
	IMAGE_ICON      = 1
	LR_LOADFROMFILE = 0x0010
	GMEM_MOVEABLE   = 0x0002
)

const (
	pageHome = iota
	pageServers
	pageSubscriptions
	pageSettings
	pageLogs
)

const (
	idNavHome = 101 + iota
	idNavServers
	idNavSubscriptions
	idNavSettings
	idNavLogs

	idConnect = 201

	idServerList = 301
	idPingAll    = 302
	idFavorite   = 303

	idSubList       = 401
	idSubURL        = 402
	idSubAdd        = 403
	idSubPaste      = 404
	idSubRefresh    = 405
	idSubDelete     = 406
	idSubRefreshAll = 407

	idModeCombo    = 501
	idSaveSettings = 502
	idOpenData     = 503
	idSupport      = 504

	idLogEdit    = 601
	idLogRefresh = 602
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	uxtheme  = syscall.NewLazyDLL("uxtheme.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	pRegisterClassEx     = user32.NewProc("RegisterClassExW")
	pCreateWindowEx      = user32.NewProc("CreateWindowExW")
	pDefWindowProc       = user32.NewProc("DefWindowProcW")
	pShowWindow          = user32.NewProc("ShowWindow")
	pUpdateWindow        = user32.NewProc("UpdateWindow")
	pGetMessage          = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessage     = user32.NewProc("DispatchMessageW")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pSendMessage         = user32.NewProc("SendMessageW")
	pPostMessage         = user32.NewProc("PostMessageW")
	pSetWindowText       = user32.NewProc("SetWindowTextW")
	pGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	pGetWindowText       = user32.NewProc("GetWindowTextW")
	pShowChild           = user32.NewProc("ShowWindow")
	pMoveWindow          = user32.NewProc("MoveWindow")
	pSetTimer            = user32.NewProc("SetTimer")
	pKillTimer           = user32.NewProc("KillTimer")
	pMessageBox          = user32.NewProc("MessageBoxW")
	pInvalidateRect      = user32.NewProc("InvalidateRect")
	pGetClientRect       = user32.NewProc("GetClientRect")
	pBeginPaint          = user32.NewProc("BeginPaint")
	pEndPaint            = user32.NewProc("EndPaint")
	pSetFocus            = user32.NewProc("SetFocus")
	pLoadImage           = user32.NewProc("LoadImageW")
	pOpenClipboard       = user32.NewProc("OpenClipboard")
	pCloseClipboard      = user32.NewProc("CloseClipboard")
	pGetClipboardData    = user32.NewProc("GetClipboardData")

	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pCreateFont       = gdi32.NewProc("CreateFontW")
	pSelectObject     = gdi32.NewProc("SelectObject")
	pFillRect         = user32.NewProc("FillRect")
	pDrawText         = user32.NewProc("DrawTextW")
	pRoundRect        = gdi32.NewProc("RoundRect")
	pCreatePen        = gdi32.NewProc("CreatePen")

	pGlobalLock            = kernel32.NewProc("GlobalLock")
	pGlobalUnlock          = kernel32.NewProc("GlobalUnlock")
	pSetWindowTheme        = uxtheme.NewProc("SetWindowTheme")
	pDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

type WNDCLASSEX struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type MSG struct {
	Hwnd    HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type RECT struct{ Left, Top, Right, Bottom int32 }
type PAINTSTRUCT struct {
	Hdc       HDC
	Erase     int32
	RcPaint   RECT
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}
type DRAWITEMSTRUCT struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   HWND
	Hdc        HDC
	RcItem     RECT
	ItemData   uintptr
}

type Window struct {
	app          *ms7app.App
	hwnd         HWND
	page         int
	controls     map[int]HWND
	pages        map[int][]HWND
	buttons      map[int]string
	serverIDs    []string
	subIDs       []string
	busy         bool
	opMu         sync.Mutex
	opErr        error
	opMessage    string
	font         HFONT
	fontBold     HFONT
	bgBrush      HBRUSH
	panelBrush   HBRUSH
	editBrush    HBRUSH
	purpleBrush  HBRUSH
	currentState model.AppState
}

var activeWindow *Window

func Run(app *ms7app.App, autoConnect string) error {
	w := &Window{
		app:          app,
		page:         pageHome,
		controls:     map[int]HWND{},
		pages:        map[int][]HWND{},
		buttons:      map[int]string{},
		currentState: app.Snapshot(),
	}
	activeWindow = w
	if err := w.init(); err != nil {
		return err
	}

	if autoConnect != "" {
		go w.runAsync("Подключение после перезапуска…", func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			_, err := app.Connect(ctx, autoConnect, "tun")
			return err
		})
	}
	go w.autoRefreshLoop()

	var msg MSG
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
	activeWindow = nil
	return nil
}

func (w *Window) init() error {
	w.bgBrush = createBrush(rgb(9, 7, 17))
	w.panelBrush = createBrush(rgb(20, 16, 32))
	w.editBrush = createBrush(rgb(16, 13, 25))
	w.purpleBrush = createBrush(rgb(100, 28, 205))
	w.font = createFont(18, 400)
	w.fontBold = createFont(21, 700)

	className := utf16Ptr("MS7VPNNativeWindow")
	title := utf16Ptr("MS7VPN")
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	wc := WNDCLASSEX{
		Size:       uint32(unsafe.Sizeof(WNDCLASSEX{})),
		Style:      0x0003,
		WndProc:    syscall.NewCallback(wndProc),
		Instance:   instance,
		Background: uintptr(w.bgBrush),
		ClassName:  className,
	}
	if r, _, e := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassExW: %v", e)
	}
	hwnd, _, e := pCreateWindowEx.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		WS_OVERLAPPEDWINDOW|WS_VISIBLE|WS_CLIPCHILDREN,
		100, 70, 1180, 760,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", e)
	}
	w.hwnd = HWND(hwnd)
	w.enableDarkTitleBar()
	w.applyIcon()
	w.createControls()
	w.showPage(pageHome)
	w.render()
	pSetTimer.Call(hwnd, 1, 1000, 0)
	pShowWindow.Call(hwnd, SW_SHOW)
	pUpdateWindow.Call(hwnd)
	return nil
}

func (w *Window) applyIcon() {
	if len(embeddedIcon) == 0 {
		return
	}
	path := filepath.Join(w.app.DataDir(), "ms7vpn.ico")
	_ = os.WriteFile(path, embeddedIcon, 0o600)
	icon, _, _ := pLoadImage.Call(0, uintptr(unsafe.Pointer(utf16Ptr(path))), IMAGE_ICON, 64, 64, LR_LOADFROMFILE)
	if icon != 0 {
		sendMessage(w.hwnd, WM_SETICON, 1, icon)
		sendMessage(w.hwnd, WM_SETICON, 0, icon)
	}
}

func (w *Window) enableDarkTitleBar() {
	value := int32(1)
	const DWMWA_USE_IMMERSIVE_DARK_MODE = 20
	pDwmSetWindowAttribute.Call(uintptr(w.hwnd), DWMWA_USE_IMMERSIVE_DARK_MODE, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
}

func (w *Window) createControls() {
	// Sidebar navigation is always available, even when no subscription exists.
	w.addButton(idNavHome, "Главная", 18, 135, 150, 44, -1)
	w.addButton(idNavServers, "Серверы", 18, 185, 150, 44, -1)
	w.addButton(idNavSubscriptions, "Подписки", 18, 235, 150, 44, -1)
	w.addButton(idNavSettings, "Настройки", 18, 285, 150, 44, -1)
	w.addButton(idNavLogs, "Журнал", 18, 335, 150, 44, -1)

	// Home.
	w.addStatic(2101, "ГЛАВНАЯ", 220, 36, 500, 36, pageHome, true)
	w.addStatic(2102, "VPN-клиент MS7VPN — подписка не блокирует доступ к интерфейсу", 220, 76, 760, 28, pageHome, false)
	w.addButton(idConnect, "ПОДКЛЮЧИТЬ", 420, 180, 280, 120, pageHome)
	w.addStatic(2103, "Выбранный сервер", 320, 335, 180, 28, pageHome, false)
	w.addStatic(2104, "— сервер не выбран —", 320, 370, 500, 42, pageHome, true)
	w.addStatic(2105, "Статус: Отключено", 320, 435, 500, 32, pageHome, false)
	w.addStatic(2106, "00:00:00", 320, 475, 300, 32, pageHome, false)
	w.addStatic(2107, "Добавить подписку можно во вкладке «Подписки». Остальные разделы доступны всегда.", 270, 565, 720, 34, pageHome, false)

	// Servers.
	w.addStatic(3101, "СЕРВЕРЫ", 220, 36, 400, 36, pageServers, true)
	w.addStatic(3102, "Только серверы из ваших добавленных подписок", 220, 76, 650, 28, pageServers, false)
	w.addList(idServerList, 220, 125, 870, 470, pageServers)
	w.addButton(idPingAll, "Проверить пинг", 220, 615, 180, 42, pageServers)
	w.addButton(idFavorite, "★ Избранное", 410, 615, 180, 42, pageServers)

	// Subscriptions.
	w.addStatic(4101, "ПОДПИСКИ", 220, 36, 400, 36, pageSubscriptions, true)
	w.addStatic(4102, "Добавьте ссылку из Telegram-бота. После импорта здесь появятся серверы.", 220, 76, 760, 28, pageSubscriptions, false)
	w.addEdit(idSubURL, "", 220, 120, 650, 40, pageSubscriptions, false)
	w.addButton(idSubPaste, "Вставить из буфера", 880, 120, 210, 40, pageSubscriptions)
	w.addButton(idSubAdd, "+ Добавить", 220, 172, 160, 42, pageSubscriptions)
	w.addButton(idSubRefreshAll, "Обновить все", 390, 172, 160, 42, pageSubscriptions)
	w.addList(idSubList, 220, 235, 870, 330, pageSubscriptions)
	w.addButton(idSubRefresh, "Обновить", 220, 585, 160, 42, pageSubscriptions)
	w.addButton(idSubDelete, "Удалить", 390, 585, 160, 42, pageSubscriptions)
	w.addStatic(4103, "", 220, 642, 870, 32, pageSubscriptions, false)

	// Settings.
	w.addStatic(5101, "НАСТРОЙКИ", 220, 36, 400, 36, pageSettings, true)
	w.addStatic(5102, "Режим подключения", 220, 125, 260, 28, pageSettings, false)
	combo := w.addCombo(idModeCombo, 220, 160, 360, 180, pageSettings)
	sendMessage(combo, CB_ADDSTRING, 0, uintptr(unsafe.Pointer(utf16Ptr("TUN — полный системный VPN"))))
	sendMessage(combo, CB_ADDSTRING, 0, uintptr(unsafe.Pointer(utf16Ptr("System Proxy — без прав администратора"))))
	w.addButton(idSaveSettings, "Сохранить", 220, 230, 160, 42, pageSettings)
	w.addButton(idOpenData, "Папка данных", 390, 230, 170, 42, pageSettings)
	w.addButton(idSupport, "Техподдержка", 570, 230, 170, 42, pageSettings)
	w.addStatic(5103, "TUN направляет системный TCP/UDP-трафик через Xray и требует запуск от администратора.\nSystem Proxy подходит для приложений, которые используют системный прокси Windows.", 220, 310, 800, 90, pageSettings, false)
	w.addStatic(5104, "Xray-core загружается с официального релиза при первом реальном подключении. Статус «Подключено» появляется только после контрольного запроса через туннель.", 220, 430, 800, 75, pageSettings, false)

	// Logs.
	w.addStatic(6101, "ЖУРНАЛ", 220, 36, 400, 36, pageLogs, true)
	w.addStatic(6102, "Логи Xray нужны для диагностики реального соединения", 220, 76, 650, 28, pageLogs, false)
	w.addEdit(idLogEdit, "", 220, 120, 870, 480, pageLogs, true)
	w.addButton(idLogRefresh, "Обновить журнал", 220, 615, 190, 42, pageLogs)

	w.addStatic(9001, "MS7VPN", 18, 28, 150, 46, -1, true)
	w.addStatic(9002, "NATIVE ALPHA", 18, 78, 150, 24, -1, false)
	w.addStatic(9003, "", 205, 690, 860, 28, -1, false)
}

func (w *Window) addControl(id int, class, text string, style uint32, x, y, cx, cy int, page int) HWND {
	hwnd, _, _ := pCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr(class))), uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(style|WS_CHILD|WS_TABSTOP), uintptr(x), uintptr(y), uintptr(cx), uintptr(cy),
		uintptr(w.hwnd), uintptr(id), 0, 0,
	)
	h := HWND(hwnd)
	w.controls[id] = h
	if page >= 0 {
		w.pages[page] = append(w.pages[page], h)
	}
	sendMessage(h, WM_SETFONT, uintptr(w.font), 1)
	theme := utf16Ptr("DarkMode_Explorer")
	pSetWindowTheme.Call(hwnd, uintptr(unsafe.Pointer(theme)), 0)
	return h
}

func (w *Window) addStatic(id int, text string, x, y, cx, cy, page int, bold bool) HWND {
	h := w.addControl(id, "STATIC", text, WS_VISIBLE, x, y, cx, cy, page)
	if bold {
		sendMessage(h, WM_SETFONT, uintptr(w.fontBold), 1)
	}
	return h
}
func (w *Window) addButton(id int, text string, x, y, cx, cy, page int) HWND {
	w.buttons[id] = text
	return w.addControl(id, "BUTTON", text, WS_VISIBLE|BS_OWNERDRAW, x, y, cx, cy, page)
}
func (w *Window) addEdit(id int, text string, x, y, cx, cy, page int, multiline bool) HWND {
	style := uint32(WS_VISIBLE | WS_BORDER | ES_AUTOHSCROLL)
	if multiline {
		style |= ES_MULTILINE | ES_AUTOVSCROLL | ES_READONLY | WS_VSCROLL
	}
	return w.addControl(id, "EDIT", text, style, x, y, cx, cy, page)
}
func (w *Window) addList(id int, x, y, cx, cy, page int) HWND {
	return w.addControl(id, "LISTBOX", "", WS_VISIBLE|WS_BORDER|WS_VSCROLL|LBS_NOTIFY|LBS_NOINTEGRALHEIGHT, x, y, cx, cy, page)
}
func (w *Window) addCombo(id int, x, y, cx, cy, page int) HWND {
	return w.addControl(id, "COMBOBOX", "", WS_VISIBLE|WS_BORDER|CBS_DROPDOWNLIST, x, y, cx, cy, page)
}

func (w *Window) showPage(page int) {
	w.page = page
	for p, controls := range w.pages {
		show := SW_HIDE
		if p == page {
			show = SW_SHOW
		}
		for _, h := range controls {
			pShowChild.Call(uintptr(h), uintptr(show))
		}
	}
	w.render()
	pInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
}

func (w *Window) render() {
	state := w.app.Snapshot()
	w.currentState = state
	w.renderHome(state)
	w.renderServers(state)
	w.renderSubscriptions(state)
	w.renderSettings(state)
	if w.page == pageLogs {
		setText(w.controls[idLogEdit], w.app.CoreLog(200))
	}
	status := "Готово"
	if w.busy {
		status = "Выполняется операция…"
	}
	if state.Core.Downloading {
		status = fmt.Sprintf("%s %d%%", state.Core.StatusMessage, state.Core.Progress)
	}
	if state.Connection.LastError != "" {
		status = "Ошибка: " + state.Connection.LastError
	}
	setText(w.controls[9003], status)
}

func (w *Window) renderHome(state model.AppState) {
	node := findNode(state, state.SelectedNodeID)
	serverText := "— сервер не выбран —"
	if node != nil {
		ping := ""
		if node.PingMS > 0 {
			ping = fmt.Sprintf(" · %d ms", node.PingMS)
		}
		serverText = fmt.Sprintf("%s%s\n%s · %s · %s", node.Name, ping, strings.ToUpper(node.Protocol), strings.ToUpper(node.Transport), strings.ToUpper(node.Security))
	}
	setText(w.controls[2104], serverText)
	status := "Статус: Отключено"
	if state.Connection.Connecting {
		status = "Статус: Подключение…"
	}
	if state.Connection.Connected {
		status = "Статус: Подключено · " + state.Connection.Mode
	}
	if state.Connection.LastError != "" {
		status = "Статус: Ошибка · " + short(state.Connection.LastError, 88)
	}
	setText(w.controls[2105], status)
	label := "ПОДКЛЮЧИТЬ"
	if state.Connection.Connected {
		label = "ОТКЛЮЧИТЬ"
	}
	if state.Connection.Connecting {
		label = "ПОДКЛЮЧЕНИЕ…"
	}
	w.buttons[idConnect] = label
	pInvalidateRect.Call(uintptr(w.controls[idConnect]), 0, 1)

	elapsed := "00:00:00"
	if state.Connection.Connected && !state.Connection.StartedAt.IsZero() {
		d := time.Since(state.Connection.StartedAt)
		if d < 0 {
			d = 0
		}
		elapsed = fmt.Sprintf("%02d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}
	setText(w.controls[2106], elapsed)
}

func (w *Window) renderServers(state model.AppState) {
	list := w.controls[idServerList]
	currentIndex := -1
	w.serverIDs = w.serverIDs[:0]
	sendMessage(list, LB_RESETCONTENT, 0, 0)
	index := 0
	for _, sub := range state.Subscriptions {
		nodes := ms7app.SortNodes(sub.Nodes, state.Favorites)
		for _, node := range nodes {
			fav := "  "
			if state.Favorites[node.ID] {
				fav = "★ "
			}
			ping := "—"
			if node.PingMS > 0 {
				ping = fmt.Sprintf("%d ms", node.PingMS)
			}
			if node.PingStatus == "error" {
				ping = "нет ответа"
			}
			supported := ""
			if !node.Supported {
				supported = " [не поддерживается]"
			}
			line := fmt.Sprintf("%s%s   |   %s   |   %s/%s/%s%s", fav, node.Name, ping, strings.ToUpper(node.Protocol), strings.ToUpper(node.Transport), strings.ToUpper(node.Security), supported)
			addListString(list, line)
			w.serverIDs = append(w.serverIDs, node.ID)
			if node.ID == state.SelectedNodeID {
				currentIndex = index
			}
			index++
		}
	}
	if currentIndex >= 0 {
		sendMessage(list, LB_SETCURSEL, uintptr(currentIndex), 0)
	}
}

func (w *Window) renderSubscriptions(state model.AppState) {
	list := w.controls[idSubList]
	currentIndex := -1
	w.subIDs = w.subIDs[:0]
	sendMessage(list, LB_RESETCONTENT, 0, 0)
	for i, sub := range state.Subscriptions {
		active := ""
		if sub.ID == state.SelectedSubID {
			active = "● "
		}
		exp := ""
		if sub.UserInfo.ExpireUnix > 0 {
			exp = " · до " + time.Unix(sub.UserInfo.ExpireUnix, 0).Format("02.01.2006")
		}
		line := fmt.Sprintf("%s%s · %d серверов · обновлено %s%s", active, sub.Name, len(sub.Nodes), relativeTime(sub.UpdatedAt), exp)
		if sub.LastError != "" {
			line += " · ОШИБКА"
		}
		addListString(list, line)
		w.subIDs = append(w.subIDs, sub.ID)
		if sub.ID == state.SelectedSubID {
			currentIndex = i
		}
	}
	if currentIndex >= 0 {
		sendMessage(list, LB_SETCURSEL, uintptr(currentIndex), 0)
	}
	info := fmt.Sprintf("Подписок: %d · Серверов: %d", len(state.Subscriptions), totalNodes(state))
	if len(state.Subscriptions) == 0 {
		info = "Подписок пока нет. Вставьте ссылку выше — весь остальной интерфейс всё равно доступен."
	}
	setText(w.controls[4103], info)
}

func (w *Window) renderSettings(state model.AppState) {
	idx := 0
	if state.Settings.ConnectionMode == "system-proxy" {
		idx = 1
	}
	sendMessage(w.controls[idModeCombo], CB_SETCURSEL, uintptr(idx), 0)
}

func (w *Window) handleCommand(wParam, lParam uintptr) {
	id := int(uint16(wParam & 0xffff))
	notify := int(uint16((wParam >> 16) & 0xffff))
	switch id {
	case idNavHome:
		w.showPage(pageHome)
	case idNavServers:
		w.showPage(pageServers)
	case idNavSubscriptions:
		w.showPage(pageSubscriptions)
	case idNavSettings:
		w.showPage(pageSettings)
	case idNavLogs:
		w.showPage(pageLogs)
	case idConnect:
		if notify == BN_CLICKED {
			w.toggleConnection()
		}
	case idSubPaste:
		if notify == BN_CLICKED {
			if text, err := clipboardText(); err == nil {
				setText(w.controls[idSubURL], strings.TrimSpace(text))
			} else {
				w.showError(err)
			}
		}
	case idSubAdd:
		if notify == BN_CLICKED {
			w.addSubscription()
		}
	case idSubRefresh:
		if notify == BN_CLICKED {
			w.refreshSelectedSubscription()
		}
	case idSubRefreshAll:
		if notify == BN_CLICKED {
			w.refreshAllSubscriptions()
		}
	case idSubDelete:
		if notify == BN_CLICKED {
			w.deleteSelectedSubscription()
		}
	case idSubList:
		if notify == LBN_SELCHANGE {
			w.selectSubscriptionFromList()
		}
	case idServerList:
		if notify == LBN_SELCHANGE {
			w.selectServerFromList()
		}
		if notify == LBN_DBLCLK {
			w.selectServerFromList()
			w.toggleConnection()
		}
	case idPingAll:
		if notify == BN_CLICKED {
			w.pingAll()
		}
	case idFavorite:
		if notify == BN_CLICKED {
			w.favoriteSelected()
		}
	case idSaveSettings:
		if notify == BN_CLICKED {
			w.saveSettings()
		}
	case idOpenData:
		if notify == BN_CLICKED {
			_ = ms7app.OpenFolder(w.app.DataDir())
		}
	case idSupport:
		if notify == BN_CLICKED {
			_ = ms7app.OpenExternalURL(w.app.Snapshot().Settings.SupportURL)
		}
	case idLogRefresh:
		if notify == BN_CLICKED {
			setText(w.controls[idLogEdit], w.app.CoreLog(200))
		}
	}
}

func (w *Window) addSubscription() {
	raw := strings.TrimSpace(getText(w.controls[idSubURL]))
	if raw == "" {
		w.showWarning("Вставьте subscription-ссылку из Telegram-бота.")
		return
	}
	w.runAsync("Добавляю подписку…", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		_, err := w.app.AddSubscription(ctx, raw, "")
		return err
	})
}

func (w *Window) refreshSelectedSubscription() {
	id := w.selectedSubID()
	if id == "" {
		w.showWarning("Выберите подписку.")
		return
	}
	w.runAsync("Обновляю подписку…", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		_, err := w.app.RefreshSubscription(ctx, id)
		return err
	})
}

func (w *Window) refreshAllSubscriptions() {
	state := w.app.Snapshot()
	if len(state.Subscriptions) == 0 {
		w.showWarning("Нет подписок для обновления.")
		return
	}
	w.runAsync("Обновляю все подписки…", func() error {
		var problems []string
		for _, sub := range state.Subscriptions {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
			_, err := w.app.RefreshSubscription(ctx, sub.ID)
			cancel()
			if err != nil {
				problems = append(problems, sub.Name+": "+err.Error())
			}
		}
		if len(problems) > 0 {
			return errors.New(strings.Join(problems, "\n"))
		}
		return nil
	})
}

func (w *Window) deleteSelectedSubscription() {
	id := w.selectedSubID()
	if id == "" {
		w.showWarning("Выберите подписку.")
		return
	}
	if messageBox(w.hwnd, "Удалить выбранную подписку?", "MS7VPN", MB_YESNO|MB_ICONWARNING) != IDYES {
		return
	}
	if _, err := w.app.DeleteSubscription(id); err != nil {
		w.showError(err)
	}
	w.render()
}

func (w *Window) selectSubscriptionFromList() {
	id := w.selectedSubID()
	if id != "" {
		w.app.SelectSubscription(id)
		w.render()
	}
}

func (w *Window) selectedSubID() string {
	idx := int(sendMessage(w.controls[idSubList], LB_GETCURSEL, 0, 0))
	if idx >= 0 && idx < len(w.subIDs) {
		return w.subIDs[idx]
	}
	return w.app.Snapshot().SelectedSubID
}

func (w *Window) selectedServerID() string {
	idx := int(sendMessage(w.controls[idServerList], LB_GETCURSEL, 0, 0))
	if idx >= 0 && idx < len(w.serverIDs) {
		return w.serverIDs[idx]
	}
	return w.app.Snapshot().SelectedNodeID
}

func (w *Window) selectServerFromList() {
	id := w.selectedServerID()
	if id == "" {
		return
	}
	if _, err := w.app.SelectNode(id); err != nil {
		w.showError(err)
		return
	}
	w.render()
}

func (w *Window) pingAll() {
	state := w.app.Snapshot()
	if totalNodes(state) == 0 {
		w.showWarning("Сначала добавьте подписку.")
		return
	}
	w.runAsync("Проверяю пинг…", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		_, err := w.app.PingNodes(ctx, "", nil)
		return err
	})
}

func (w *Window) favoriteSelected() {
	id := w.selectedServerID()
	if id == "" {
		w.showWarning("Выберите сервер.")
		return
	}
	if _, err := w.app.ToggleFavorite(id); err != nil {
		w.showError(err)
		return
	}
	w.render()
}

func (w *Window) toggleConnection() {
	if w.busy {
		return
	}
	state := w.app.Snapshot()
	if state.Connection.Connected || state.Connection.Connecting {
		w.runAsync("Отключение…", func() error { _, err := w.app.Disconnect(); return err })
		return
	}
	if state.SelectedNodeID == "" {
		w.showWarning("Выберите сервер во вкладке «Серверы». Если список пуст — сначала добавьте подписку.")
		return
	}
	w.runAsync("Реальное подключение через Xray…", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, err := w.app.Connect(ctx, state.SelectedNodeID, state.Settings.ConnectionMode)
		var apiErr *ms7app.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "NEED_ADMIN" {
			if elevateErr := ms7app.RelaunchElevated(state.SelectedNodeID); elevateErr != nil {
				return elevateErr
			}
			pPostMessage.Call(uintptr(w.hwnd), WM_CLOSE, 0, 0)
			return nil
		}
		return err
	})
}

func (w *Window) saveSettings() {
	state := w.app.Snapshot()
	idx := int(sendMessage(w.controls[idModeCombo], CB_GETCURSEL, 0, 0))
	if idx == 1 {
		state.Settings.ConnectionMode = "system-proxy"
	} else {
		state.Settings.ConnectionMode = "tun"
	}
	if _, err := w.app.SaveSettings(state.Settings); err != nil {
		w.showError(err)
		return
	}
	w.opMessage = "Настройки сохранены"
	w.render()
}

func (w *Window) runAsync(message string, fn func() error) {
	w.opMu.Lock()
	if w.busy {
		w.opMu.Unlock()
		return
	}
	w.busy = true
	w.opMessage = message
	w.opErr = nil
	w.opMu.Unlock()
	w.render()
	go func() {
		err := fn()
		w.opMu.Lock()
		w.busy = false
		w.opErr = err
		if err == nil {
			w.opMessage = "Готово"
		}
		w.opMu.Unlock()
		pPostMessage.Call(uintptr(w.hwnd), WM_APP+1, 0, 0)
	}()
}

func (w *Window) autoRefreshLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-w.app.Done():
			return
		case <-ticker.C:
			if w.busy {
				continue
			}
			state := w.app.Snapshot()
			if !state.Settings.AutoRefresh {
				continue
			}
			now := time.Now()
			for _, sub := range state.Subscriptions {
				interval := sub.UpdateIntervalMinutes
				if interval <= 0 {
					interval = 60
				}
				if now.Sub(sub.UpdatedAt) < time.Duration(interval)*time.Minute {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
				_, _ = w.app.RefreshSubscription(ctx, sub.ID)
				cancel()
			}
			pPostMessage.Call(uintptr(w.hwnd), WM_APP+2, 0, 0)
		}
	}
}

func (w *Window) onAsyncDone() {
	w.opMu.Lock()
	err := w.opErr
	w.opErr = nil
	w.opMu.Unlock()
	if err != nil {
		w.showError(err)
	}
	w.render()
}

func (w *Window) showError(err error) {
	if err != nil {
		messageBox(w.hwnd, err.Error(), "MS7VPN — ошибка", MB_OK|MB_ICONERROR)
	}
}
func (w *Window) showWarning(text string) { messageBox(w.hwnd, text, "MS7VPN", MB_OK|MB_ICONWARNING) }

func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	w := activeWindow
	if w == nil {
		r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	}
	switch msg {
	case WM_COMMAND:
		w.handleCommand(wParam, lParam)
		return 0
	case WM_TIMER:
		w.render()
		return 0
	case WM_APP + 1:
		w.onAsyncDone()
		return 0
	case WM_APP + 2:
		w.render()
		return 0
	case WM_DRAWITEM:
		ds := (*DRAWITEMSTRUCT)(unsafe.Pointer(lParam))
		if ds != nil {
			w.drawButton(ds)
			return 1
		}
	case WM_CTLCOLORSTATIC:
		hdc := HDC(wParam)
		if HWND(lParam) == w.controls[9001] {
			pSetTextColor.Call(uintptr(hdc), rgb(150, 48, 240))
		} else {
			pSetTextColor.Call(uintptr(hdc), rgb(230, 225, 245))
		}
		pSetBkMode.Call(uintptr(hdc), TRANSPARENT)
		return uintptr(w.bgBrush)
	case WM_CTLCOLOREDIT, WM_CTLCOLORLISTBOX:
		hdc := HDC(wParam)
		pSetTextColor.Call(uintptr(hdc), rgb(238, 235, 247))
		pSetBkColor.Call(uintptr(hdc), rgb(16, 13, 25))
		return uintptr(w.editBrush)
	case WM_CTLCOLORBTN:
		return uintptr(w.panelBrush)
	case WM_PAINT:
		var ps PAINTSTRUCT
		hdc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		var rc RECT
		pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), uintptr(w.bgBrush))
		// Sidebar background.
		sidebar := RECT{0, 0, 190, rc.Bottom}
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&sidebar)), uintptr(w.panelBrush))
		pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case WM_CLOSE:
		w.app.Shutdown()
		pKillTimer.Call(hwnd, 1)
		user32.NewProc("DestroyWindow").Call(hwnd)
		return 0
	case WM_DESTROY:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func (w *Window) drawButton(ds *DRAWITEMSTRUCT) {
	id := int(ds.CtlID)
	label := w.buttons[id]
	selected := (id == idNavHome && w.page == pageHome) || (id == idNavServers && w.page == pageServers) || (id == idNavSubscriptions && w.page == pageSubscriptions) || (id == idNavSettings && w.page == pageSettings) || (id == idNavLogs && w.page == pageLogs)
	isConnect := id == idConnect
	bg := w.panelBrush
	if selected || isConnect {
		bg = w.purpleBrush
	}
	pFillRect.Call(uintptr(ds.Hdc), uintptr(unsafe.Pointer(&ds.RcItem)), uintptr(bg))
	pSetBkMode.Call(uintptr(ds.Hdc), TRANSPARENT)
	pSetTextColor.Call(uintptr(ds.Hdc), rgb(245, 242, 255))
	font := w.font
	if isConnect {
		font = w.fontBold
	}
	old, _, _ := pSelectObject.Call(uintptr(ds.Hdc), uintptr(font))
	txt := utf16Ptr(label)
	rc := ds.RcItem
	pDrawText.Call(uintptr(ds.Hdc), uintptr(unsafe.Pointer(txt)), uintptr(^uint32(0)), uintptr(unsafe.Pointer(&rc)), DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	pSelectObject.Call(uintptr(ds.Hdc), old)
}

func createBrush(color uintptr) HBRUSH { r, _, _ := pCreateSolidBrush.Call(color); return HBRUSH(r) }
func createFont(height int32, weight int32) HFONT {
	face := utf16Ptr("Segoe UI")
	r, _, _ := pCreateFont.Call(uintptr(-height), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 4, 0, 0, 0, uintptr(unsafe.Pointer(face)))
	return HFONT(r)
}
func rgb(r, g, b byte) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }
func sendMessage(hwnd HWND, msg uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := pSendMessage.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}
func setText(hwnd HWND, text string) {
	if hwnd != 0 {
		pSetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16Ptr(text))))
	}
}
func getText(hwnd HWND) string {
	n, _, _ := pGetWindowTextLength.Call(uintptr(hwnd))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	pGetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}
func addListString(hwnd HWND, text string) {
	p := utf16Ptr(text)
	sendMessage(hwnd, LB_ADDSTRING, 0, uintptr(unsafe.Pointer(p)))
}
func utf16Ptr(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func messageBox(hwnd HWND, text, title string, flags uintptr) int {
	r, _, _ := pMessageBox.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16Ptr(text))), uintptr(unsafe.Pointer(utf16Ptr(title))), flags)
	return int(r)
}

func clipboardText() (string, error) {
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return "", errors.New("не удалось открыть буфер обмена")
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(CF_UNICODETEXT)
	if h == 0 {
		return "", errors.New("в буфере нет текста")
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		return "", errors.New("не удалось прочитать буфер обмена")
	}
	defer pGlobalUnlock.Call(h)
	// Scan a bounded UTF-16 string.
	data := make([]uint16, 0, 8192)
	for i := 0; i < 8192; i++ {
		v := *(*uint16)(unsafe.Pointer(ptr + uintptr(i*2)))
		if v == 0 {
			break
		}
		data = append(data, v)
	}
	if len(data) == 0 {
		return "", errors.New("буфер пуст")
	}
	return string(utf16.Decode(data)), nil
}

func findNode(state model.AppState, id string) *model.Node {
	for si := range state.Subscriptions {
		for ni := range state.Subscriptions[si].Nodes {
			if state.Subscriptions[si].Nodes[ni].ID == id {
				n := state.Subscriptions[si].Nodes[ni]
				return &n
			}
		}
	}
	return nil
}
func totalNodes(state model.AppState) int {
	n := 0
	for _, s := range state.Subscriptions {
		n += len(s.Nodes)
	}
	return n
}
func short(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "никогда"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return "только что"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	}
	return t.Format("02.01.2006 15:04")
}
