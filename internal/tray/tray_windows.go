//go:build windows

// Package tray показывает значок MS7VPN в области уведомлений Windows
// (правый нижний угол, рядом с часами).
//
// Раньше значка не было вообще: приложение существовало только как окно,
// и, свернув его, пользователь терял программу из виду.
package tray

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Значки состояния: фирменная буква M в трёх цветах.
//
//go:embed icons/*.png
var iconFiles embed.FS

// Состояние, которое показывает значок.
type State int

const (
	StateOff State = iota
	StateConnecting
	StateOn
)

type Handlers struct {
	OnToggleConnect func()
	OnShowWindow    func()
	OnRefreshSubs   func()
	OnQuit          func()
}

// Константы оконных сообщений: в golang.org/x/sys/windows их нет.
const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	wmApp           = 0x8000
)

const (
	messageTrayCallback = wmApp + 1

	menuConnect = 1001
	menuShow    = 1002
	menuRefresh = 1003
	menuQuit    = 1004
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessage      = user32.NewProc("PostMessageW")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenu       = user32.NewProc("AppendMenuW")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procSetForegroundWin = user32.NewProc("SetForegroundWindow")
	procCreateIcon       = user32.NewProc("CreateIconIndirect")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procDestroyIcon      = user32.NewProc("DestroyIcon")
	procShellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
	procCreateDIBSection = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap     = gdi32.NewProc("CreateBitmap")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
	procGetModuleHandle  = kernel32.NewProc("GetModuleHandleW")
)

// NOTIFYICONDATAW. Раскладка полей повторяет структуру из shellapi.h;
// корректность размера проверяется тестом TestNotifyIconDataLayout.
type notifyIconData struct {
	CbSize           uint32
	HWnd             windows.HWND
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

const (
	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo = 0x00000001
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type point struct{ X, Y int32 }

type msg struct {
	HWnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type Tray struct {
	handlers Handlers
	window   windows.HWND
	icons    map[State]windows.Handle
	mu       sync.Mutex
	state    State
	tip      string
	added    bool
	closed   bool
}

var (
	instanceMu sync.Mutex
	instance   *Tray
	// Сообщение, которым вторая копия просит показать уже открытое окно.
	showMessage uint32
)

// SetShowMessage задаёт идентификатор broadcast-сообщения «показать окно».
func SetShowMessage(value uint32) { showMessage = value }

// New создаёт значок. Возвращает ошибку, если Windows не дала создать окно:
// в этом случае приложение просто работает без значка, а не падает.
func New(handlers Handlers) (*Tray, error) {
	t := &Tray{handlers: handlers, icons: map[State]windows.Handle{}, tip: "MS7VPN"}

	instanceMu.Lock()
	instance = t
	instanceMu.Unlock()

	className, err := windows.UTF16PtrFromString("MS7VPNTrayWindow")
	if err != nil {
		return nil, err
	}
	moduleHandle, _, _ := procGetModuleHandle.Call(0)
	class := wndClassEx{
		Instance:  windows.Handle(moduleHandle),
		WndProc:   windows.NewCallback(trayWndProc),
		ClassName: className,
	}
	class.Size = uint32(unsafe.Sizeof(class))
	if atom, _, callErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return nil, fmt.Errorf("RegisterClassEx: %v", callErr)
	}

	// Окно скрытое и служебное: оно нужно только чтобы принимать сообщения
	// от значка в области уведомлений.
	windowName, _ := windows.UTF16PtrFromString("MS7VPN")
	handle, _, callErr := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0, 0, 0, 0, 0, 0, 0,
		moduleHandle, 0,
	)
	if handle == 0 {
		return nil, fmt.Errorf("CreateWindowEx: %v", callErr)
	}
	t.window = windows.HWND(handle)

	size := smallIconSize()
	t.icons[StateOff] = loadStateIcon("off", size)
	t.icons[StateConnecting] = loadStateIcon("connecting", size)
	t.icons[StateOn] = loadStateIcon("on", size)

	if err := t.send(nimAdd, nifMessage|nifIcon|nifTip, "", ""); err != nil {
		return nil, err
	}
	t.added = true
	return t, nil
}

// Run крутит цикл сообщений значка. Выполняется в отдельной горутине,
// привязанной к своему потоку ОС: у окна WebView2 свой цикл сообщений.
func (t *Tray) Run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var message msg
	for {
		result, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func (t *Tray) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	added := t.added
	t.mu.Unlock()

	if added {
		_ = t.send(nimDelete, 0, "", "")
	}
	for _, icon := range t.icons {
		if icon != 0 {
			procDestroyIcon.Call(uintptr(icon))
		}
	}
	if t.window != 0 {
		procPostMessage.Call(uintptr(t.window), wmClose, 0, 0)
	}
}

// SetState меняет цвет значка и подсказку при наведении.
func (t *Tray) SetState(state State, tip string) {
	t.mu.Lock()
	if t.closed || (t.state == state && t.tip == tip) {
		t.mu.Unlock()
		return
	}
	t.state, t.tip = state, tip
	t.mu.Unlock()
	_ = t.send(nimModify, nifIcon|nifTip, "", "")
}

// Notify показывает всплывающее уведомление Windows.
func (t *Tray) Notify(title, text string) {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return
	}
	_ = t.send(nimModify, nifInfo, title, text)
}

func (t *Tray) send(action, flags uint32, title, text string) error {
	t.mu.Lock()
	state, tip := t.state, t.tip
	t.mu.Unlock()

	data := notifyIconData{
		HWnd:             t.window,
		UID:              1,
		UFlags:           flags,
		UCallbackMessage: messageTrayCallback,
		HIcon:            t.icons[state],
		DwInfoFlags:      niifInfo,
	}
	data.CbSize = uint32(unsafe.Sizeof(data))
	copyUTF16(data.SzTip[:], tip)
	copyUTF16(data.SzInfoTitle[:], title)
	copyUTF16(data.SzInfo[:], text)

	result, _, callErr := procShellNotifyIcon.Call(uintptr(action), uintptr(unsafe.Pointer(&data)))
	if result == 0 {
		return fmt.Errorf("Shell_NotifyIcon(%d): %v", action, callErr)
	}
	return nil
}

func copyUTF16(destination []uint16, value string) {
	for i := range destination {
		destination[i] = 0
	}
	if value == "" {
		return
	}
	encoded := windows.StringToUTF16(value)
	if len(encoded) > len(destination) {
		encoded = encoded[:len(destination)]
		encoded[len(encoded)-1] = 0
	}
	copy(destination, encoded)
}

func trayWndProc(handle, message, wParam, lParam uintptr) uintptr {
	instanceMu.Lock()
	t := instance
	instanceMu.Unlock()
	if t == nil {
		result, _, _ := procDefWindowProc.Call(handle, message, wParam, lParam)
		return result
	}

	if showMessage != 0 && message == uintptr(showMessage) {
		t.call(t.handlers.OnShowWindow)
		return 0
	}

	switch message {
	case messageTrayCallback:
		switch uint32(lParam) {
		case wmLButtonUp, wmLButtonDblClk:
			t.call(t.handlers.OnShowWindow)
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		}
		return 0
	case wmCommand:
		switch uint32(wParam) & 0xFFFF {
		case menuConnect:
			t.call(t.handlers.OnToggleConnect)
		case menuShow:
			t.call(t.handlers.OnShowWindow)
		case menuRefresh:
			t.call(t.handlers.OnRefreshSubs)
		case menuQuit:
			t.call(t.handlers.OnQuit)
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(handle)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProc.Call(handle, message, wParam, lParam)
	return result
}

// call выполняет обработчик в отдельной горутине: он ходит в приложение и
// может занять секунды, а цикл сообщений блокировать нельзя.
func (t *Tray) call(handler func()) {
	if handler == nil {
		return
	}
	go handler()
}

func (t *Tray) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	t.mu.Lock()
	state := t.state
	t.mu.Unlock()

	connectLabel := "Подключить"
	if state == StateOn {
		connectLabel = "Отключить"
	} else if state == StateConnecting {
		connectLabel = "Идёт подключение…"
	}

	appendMenuItem(menu, menuConnect, connectLabel)
	appendSeparator(menu)
	appendMenuItem(menu, menuShow, "Открыть окно")
	appendMenuItem(menu, menuRefresh, "Обновить подписку")
	appendSeparator(menu)
	appendMenuItem(menu, menuQuit, "Выход")

	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	// Меню исчезает при клике мимо только если наше окно на переднем плане.
	procSetForegroundWin.Call(uintptr(t.window))
	const tpmRightButton = 0x0002
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(cursor.X), uintptr(cursor.Y), 0, uintptr(t.window), 0)
	procPostMessage.Call(uintptr(t.window), 0, 0, 0)
}

func appendMenuItem(menu uintptr, id uint32, label string) {
	text, err := windows.UTF16PtrFromString(label)
	if err != nil {
		return
	}
	const mfString = 0x00000000
	procAppendMenu.Call(menu, mfString, uintptr(id), uintptr(unsafe.Pointer(text)))
}

func appendSeparator(menu uintptr) {
	const mfSeparator = 0x00000800
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  windows.Handle
	HbmColor windows.Handle
}

// smallIconSize — размер значка в области уведомлений для текущего масштаба
// экрана: 16 px при 100 %, 24 при 150 %, 32 при 200 %.
func smallIconSize() int {
	const smCXSMICON = 49
	value, _, _ := procGetSystemMetrics.Call(uintptr(smCXSMICON))
	size := int(value)
	if size < 16 || size > 64 {
		return 16
	}
	return size
}

// loadStateIcon готовит HICON из встроенного PNG нужного состояния.
func loadStateIcon(name string, size int) windows.Handle {
	data, err := iconFiles.ReadFile("icons/" + name + ".png")
	if err != nil {
		return 0
	}
	source, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return 0
	}
	return iconFromImage(downscale(source, size), size)
}

// downscale уменьшает картинку усреднением по площади. Значки идут в 64 px,
// а нужны 16–32, поэтому усреднение даёт заметно более чистый край, чем
// выбор ближайшего пикселя.
func downscale(source image.Image, size int) []uint32 {
	bounds := source.Bounds()
	result := make([]uint32, size*size)
	scaleX := float64(bounds.Dx()) / float64(size)
	scaleY := float64(bounds.Dy()) / float64(size)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			startX := bounds.Min.X + int(float64(x)*scaleX)
			endX := bounds.Min.X + int(float64(x+1)*scaleX)
			startY := bounds.Min.Y + int(float64(y)*scaleY)
			endY := bounds.Min.Y + int(float64(y+1)*scaleY)
			if endX <= startX {
				endX = startX + 1
			}
			if endY <= startY {
				endY = startY + 1
			}

			var sumR, sumG, sumB, sumA, count uint64
			for sy := startY; sy < endY && sy < bounds.Max.Y; sy++ {
				for sx := startX; sx < endX && sx < bounds.Max.X; sx++ {
					// RGBA() уже возвращает premultiplied значения в 16 битах —
					// ровно то, что нужно 32-битному значку Windows.
					r, g, b, a := source.At(sx, sy).RGBA()
					sumR += uint64(r >> 8)
					sumG += uint64(g >> 8)
					sumB += uint64(b >> 8)
					sumA += uint64(a >> 8)
					count++
				}
			}
			if count == 0 {
				continue
			}
			result[y*size+x] = uint32(sumA/count)<<24 |
				uint32(sumR/count)<<16 |
				uint32(sumG/count)<<8 |
				uint32(sumB/count)
		}
	}
	return result
}

// iconFromImage собирает HICON из массива пикселей BGRA.
func iconFromImage(pixels []uint32, size int) windows.Handle {
	header := bitmapInfoHeader{
		Width:    int32(size),
		Height:   -int32(size), // сверху вниз
		Planes:   1,
		BitCount: 32,
	}
	header.Size = uint32(unsafe.Sizeof(header))

	var bits unsafe.Pointer
	bitmap, _, _ := procCreateDIBSection.Call(
		0,
		uintptr(unsafe.Pointer(&header)),
		0, // DIB_RGB_COLORS
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if bitmap == 0 || bits == nil {
		return 0
	}
	copy(unsafe.Slice((*uint32)(bits), size*size), pixels)

	mask, _, _ := procCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, 0)
	info := iconInfo{FIcon: 1, HbmMask: windows.Handle(mask), HbmColor: windows.Handle(bitmap)}
	icon, _, _ := procCreateIcon.Call(uintptr(unsafe.Pointer(&info)))

	procDeleteObject.Call(bitmap)
	procDeleteObject.Call(mask)
	if icon == 0 {
		return 0
	}
	return windows.Handle(icon)
}
