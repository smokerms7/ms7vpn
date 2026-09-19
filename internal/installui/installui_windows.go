//go:build windows

// Package installui рисует окно мастера установки.
//
// Вид классический, в духе привычных установщиков Windows: слева баннер с
// логотипом, справа страницы, внизу «Назад», «Далее» и «Отмена». Страницы
// идут по порядку: приветствие, папка, подтверждение, установка, завершение.
//
// Окно намеренно сделано на голом Win32 и GDI, без WebView2, которым рисуется
// сама программа: на момент установки WebView2 в системе может не быть — его
// как раз и ставит установщик. Мастер на WebView2 в такой системе просто не
// открылся бы.
//
// Кнопки — обычные системные, не рисованные: в светлом оформлении они и
// должны выглядеть как везде в Windows. Своими руками рисуется только то,
// чего в системе нет: баннер и полоса прогресса.
package installui

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// bannerLogo — белый знак MS7 для тёмного баннера.
//
//go:embed banner-logo.png
var bannerLogo []byte

// Цвета. Страницы светлые, как у привычных установщиков; тёмный остаётся
// только на баннере, где он и держит фирменный вид программы.
const (
	colorPage      = 0xFFFFFF // белый фон страниц
	colorBar       = 0xF0F0F0 // полоса с кнопками внизу
	colorBarLine   = 0xDFDFDF // её верхняя граница
	colorInk       = 0x000000 // основной текст
	colorMuted     = 0x606060 // пояснения
	colorBanner    = 0x1B0A14 // #140A1B, фон баннера
	colorBannerLow = 0x0C0708 // #08070C, низ градиента
	colorAccent    = 0xFF26B0 // #B026FF, фиолетовый акцент
	colorBullet    = 0xFF26B0 // #B026FF, точки на баннере — фирменный фиолетовый
	colorTrack     = 0xE4E4E4 // дорожка полосы прогресса
	colorBad       = 0x2222CC // красный для ошибки
	colorWarn      = 0x1A6BB5 // янтарный для предупреждения
)

// Размеры в логических точках при 96 DPI; всё остальное считается от них.
const (
	winWidth  = 500
	winHeight = 368

	bannerWidth = 164
	pageBottom  = 320
	barHeight   = winHeight - pageBottom

	headerHeight = 58 // шапка внутренних страниц

	buttonWidth  = 88
	buttonHeight = 28
)

// Request описывает, что показать в мастере.
type Request struct {
	// Caption — заголовок окна.
	Caption string
	// Product — название программы в тексте страниц.
	Product string
	// Version — номер версии.
	Version string
	// DefaultDir — папка установки, предложенная по умолчанию.
	DefaultDir string
	// Hint — пояснение на странице подтверждения. Пустое не показывается.
	Hint string
	// AllowChooseDir — показывать страницу выбора папки.
	AllowChooseDir bool
	// Bullets — короткие строки на баннере, не больше четырёх.
	Bullets []string
	// AutoStart — сразу перейти к установке, минуя страницы.
	// Так приложение обновляет само себя: человек уже согласился в окне
	// программы, второй раз спрашивать незачем.
	AutoStart bool
	// Upgrade — установка поверх уже имеющейся версии. Меняет только текст.
	Upgrade bool
	// ValidateDir проверяет выбранную папку. Непустая строка — текст ошибки,
	// которую увидит человек; дальше мастер не пустит.
	ValidateDir func(dir string) string
}

// Reporter сообщает мастеру о ходе установки. Его можно звать из любой
// горутины: сообщения ставятся в очередь окна.
type Reporter func(percent int, text string)

// InstallFunc выполняет саму установку. Вызывается в отдельной горутине,
// чтобы окно не переставало отвечать.
//
// Возвращает предупреждение и ошибку по отдельности: установка может пройти
// и при этом оставить недоделку, о которой человеку стоит знать. Раньше
// такой случай приходилось выдавать за ошибку, и на последней странице
// красным горело «Установка не завершена», хотя программа была установлена
// и работала.
type InstallFunc func(dir string, report Reporter) (warning string, err error)

// Outcome — чем закончился разговор с человеком.
type Outcome struct {
	Installed bool
	Launch    bool
	Dir       string
	Err       error
}

// ---------------------------------------------------------------- Win32

type hwnd uintptr

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	msimg32  = windows.NewLazySystemDLL("msimg32.dll")

	pRegisterClassEx  = user32.NewProc("RegisterClassExW")
	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pShowWindow       = user32.NewProc("ShowWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pGetMessage       = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pPostMessage      = user32.NewProc("PostMessageW")
	pSendMessage      = user32.NewProc("SendMessageW")
	pSetWindowText    = user32.NewProc("SetWindowTextW")
	pGetWindowText    = user32.NewProc("GetWindowTextW")
	pGetWindowTextLen = user32.NewProc("GetWindowTextLengthW")
	pMoveWindow       = user32.NewProc("MoveWindow")
	pInvalidateRect   = user32.NewProc("InvalidateRect")
	pGetClientRect    = user32.NewProc("GetClientRect")
	pBeginPaint       = user32.NewProc("BeginPaint")
	pEndPaint         = user32.NewProc("EndPaint")
	pFillRect         = user32.NewProc("FillRect")
	pDrawText         = user32.NewProc("DrawTextW")
	pSetFocus         = user32.NewProc("SetFocus")
	pLoadImage        = user32.NewProc("LoadImageW")
	pDrawIconEx       = user32.NewProc("DrawIconEx")
	pMessageBox       = user32.NewProc("MessageBoxW")
	pSetForeground    = user32.NewProc("SetForegroundWindow")
	pSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pSetProcessDPI    = user32.NewProc("SetProcessDPIAware")
	pEnableWindow     = user32.NewProc("EnableWindow")
	pAdjustWindowRect = user32.NewProc("AdjustWindowRect")
	pGetDC            = user32.NewProc("GetDC")
	pReleaseDC        = user32.NewProc("ReleaseDC")

	pCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pSetTextColor       = gdi32.NewProc("SetTextColor")
	pSetBkMode          = gdi32.NewProc("SetBkMode")
	pCreateFont         = gdi32.NewProc("CreateFontW")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pGetDeviceCaps      = gdi32.NewProc("GetDeviceCaps")
	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pEllipse            = gdi32.NewProc("Ellipse")
	pCreatePen          = gdi32.NewProc("CreatePen")
	pRectangle          = gdi32.NewProc("Rectangle")

	pAlphaBlend   = msimg32.NewProc("AlphaBlend")
	pGradientFill = msimg32.NewProc("GradientFill")

	pGetModuleHandle = kernel32.NewProc("GetModuleHandleW")

	pSHBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	pCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	pCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
)

const (
	wsOverlapped  = 0x00000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsChild       = 0x40000000
	wsTabStop     = 0x00010000
	wsClipChild   = 0x02000000
	wsBorder      = 0x00800000

	esAutoHScroll   = 0x0080
	bsAutoCheckBox  = 0x00000003
	bsDefPushButton = 0x00000001

	bmSetCheck = 0x00F1
	bmGetCheck = 0x00F0
	bstChecked = 1

	swHide = 0
	swShow = 5

	wmDestroy        = 0x0002
	wmPaint          = 0x000F
	wmClose          = 0x0010
	wmSetFont        = 0x0030
	wmCommand        = 0x0111
	wmCtlColorEdit   = 0x0133
	wmCtlColorStatic = 0x0138
	wmCtlColorBtn    = 0x0135
	wmApp            = 0x8000

	wmProgress = wmApp + 1
	wmFinished = wmApp + 2

	dtLeft        = 0x00000000
	dtCenter      = 0x00000001
	dtVCenter     = 0x00000004
	dtWordBreak   = 0x00000010
	dtSingleLine  = 0x00000020
	dtEndEllipsis = 0x00008000
	dtNoPrefix    = 0x00000800

	transparent = 1
	psSolid     = 0
	logPixelsX  = 88
	imageIcon   = 1

	mbOK       = 0x00000000
	mbIconWarn = 0x00000030

	idBack   = 1001
	idNext   = 1002
	idCancel = 1003
	idBrowse = 1004
	idPath   = 1005
	idLaunch = 1006
)

type rect struct{ Left, Top, Right, Bottom int32 }

type point struct{ X, Y int32 }

type msg struct {
	HWND    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
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

type paintStruct struct {
	HDC         uintptr
	Erase       int32
	Paint       rect
	Restore     int32
	IncUpdate   int32
	RgbReserved [32]byte
}

type browseInfo struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Callback    uintptr
	LParam      uintptr
	Image       int32
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

type trivertex struct {
	X     int32
	Y     int32
	Red   uint16
	Green uint16
	Blue  uint16
	Alpha uint16
}

type gradientRect struct {
	UpperLeft  uint32
	LowerRight uint32
}

func utf16ptr(value string) *uint16 {
	pointer, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		empty, _ := syscall.UTF16PtrFromString("")
		return empty
	}
	return pointer
}

// ---------------------------------------------------------------- страницы

const (
	pageWelcome = iota
	pageDir
	pageReady
	pageInstall
	pageFinish
)

type wizard struct {
	request Request
	install InstallFunc

	window   hwnd
	pathEdit hwnd
	controls map[uintptr]hwnd

	fontBody  uintptr
	fontBold  uintptr
	fontBig   uintptr
	brushPage uintptr
	brushBar  uintptr
	icon      uintptr
	logo      *bitmap

	scale float64

	mu      sync.Mutex
	page    int
	percent int
	status  string
	failed  bool
	failure string
	warning string

	outcome Outcome
	started bool
}

var active *wizard

// Run показывает мастер и возвращается, когда окно закрыто.
//
// Вся работа с окном идёт в одном потоке операционной системы: Win32 этого
// требует, а Go без LockOSThread свободно переносит горутину между потоками.
func Run(request Request, install InstallFunc) Outcome {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// SHBrowseForFolder с современным видом окна требует инициализации COM.
	pCoInitializeEx.Call(0, 0x2 /* COINIT_APARTMENTTHREADED */)
	pSetProcessDPI.Call()

	w := &wizard{
		request:  request,
		install:  install,
		controls: map[uintptr]hwnd{},
		scale:    1,
		status:   "Подготовка...",
	}
	w.outcome.Dir = request.DefaultDir
	active = w
	defer func() { active = nil }()

	if err := w.create(); err != nil {
		return Outcome{Err: err}
	}
	w.pump()
	return w.outcome
}

func (w *wizard) dpi(value int) int32 { return int32(float64(value)*w.scale + 0.5) }

func (w *wizard) create() error {
	instance, _, _ := pGetModuleHandle.Call(0)

	if screenDC, _, _ := pGetDC.Call(0); screenDC != 0 {
		pixels, _, _ := pGetDeviceCaps.Call(screenDC, logPixelsX)
		pReleaseDC.Call(0, screenDC)
		if pixels > 0 {
			w.scale = float64(pixels) / 96.0
		}
	}

	w.brushPage = solidBrush(colorPage)
	w.brushBar = solidBrush(colorBar)
	w.fontBody = w.font(12, 400)
	w.fontBold = w.font(12, 700)
	w.fontBig = w.font(17, 700)
	w.logo = loadPNG(bannerLogo)

	if handle, _, _ := pLoadImage.Call(instance, 1, imageIcon, 0, 0, 0); handle != 0 {
		w.icon = handle
	}

	className := utf16ptr("MS7VPNInstallerWizard")
	class := wndClassEx{
		Size:       uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:    syscall.NewCallback(windowProc),
		Instance:   instance,
		Background: w.brushPage,
		ClassName:  className,
		Icon:       w.icon,
		IconSm:     w.icon,
	}
	if result, _, err := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); result == 0 {
		return fmt.Errorf("не удалось создать окно установщика: %w", err)
	}

	// Размер задаём по рабочей области, а не по всему окну: иначе рамка и
	// заголовок съедают часть страницы, и на разных версиях Windows
	// по-разному.
	const style = wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox | wsClipChild
	area := rect{Right: w.dpi(winWidth), Bottom: w.dpi(winHeight)}
	pAdjustWindowRect.Call(uintptr(unsafe.Pointer(&area)), style, 0)
	width := area.Right - area.Left
	height := area.Bottom - area.Top

	screenW, _, _ := pSystemMetrics.Call(0)
	screenH, _, _ := pSystemMetrics.Call(1)
	x := (int32(screenW) - width) / 2
	y := (int32(screenH) - height) / 2

	handle, _, err := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(utf16ptr(w.request.Caption))),
		style,
		uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		0, 0, instance, 0)
	if handle == 0 {
		return fmt.Errorf("не удалось создать окно установщика: %w", err)
	}
	w.window = hwnd(handle)

	w.buildControls(instance)

	if w.request.AutoStart {
		w.page = pageInstall
		w.applyPage()
		pShowWindow.Call(handle, swShow)
		pUpdateWindow.Call(handle)
		pSetForeground.Call(handle)
		w.begin()
		return nil
	}

	w.applyPage()
	pShowWindow.Call(handle, swShow)
	pUpdateWindow.Call(handle)
	pSetForeground.Call(handle)
	return nil
}

func solidBrush(color uint32) uintptr {
	handle, _, _ := pCreateSolidBrush.Call(uintptr(color))
	return handle
}

func (w *wizard) font(size, weight int) uintptr {
	handle, _, _ := pCreateFont.Call(
		uintptr(-w.dpi(size)), 0, 0, 0, uintptr(weight),
		0, 0, 0, 1 /* DEFAULT_CHARSET */, 0, 0, 5 /* CLEARTYPE_QUALITY */, 0,
		uintptr(unsafe.Pointer(utf16ptr("Segoe UI"))))
	return handle
}

func (w *wizard) buildControls(instance uintptr) {
	create := func(class, caption string, style uint32, id uintptr, x, y, cx, cy int) hwnd {
		handle, _, _ := pCreateWindowEx.Call(0,
			uintptr(unsafe.Pointer(utf16ptr(class))),
			uintptr(unsafe.Pointer(utf16ptr(caption))),
			uintptr(wsChild|wsTabStop|style),
			uintptr(w.dpi(x)), uintptr(w.dpi(y)), uintptr(w.dpi(cx)), uintptr(w.dpi(cy)),
			uintptr(w.window), id, instance, 0)
		pSendMessage.Call(handle, wmSetFont, w.fontBody, 1)
		w.controls[id] = hwnd(handle)
		return hwnd(handle)
	}

	barY := pageBottom + (barHeight-buttonHeight)/2
	cancelX := winWidth - 16 - buttonWidth
	nextX := cancelX - 12 - buttonWidth
	backX := nextX - buttonWidth

	create("BUTTON", "< Назад", 0, idBack, backX, barY, buttonWidth, buttonHeight)
	create("BUTTON", "Далее >", bsDefPushButton, idNext, nextX, barY, buttonWidth, buttonHeight)
	create("BUTTON", "Отмена", 0, idCancel, cancelX, barY, buttonWidth, buttonHeight)

	w.pathEdit = create("EDIT", w.request.DefaultDir, wsBorder|esAutoHScroll, idPath,
		24, 116, winWidth-24-24-90-8, 26)
	create("BUTTON", "Обзор...", 0, idBrowse, winWidth-24-90, 116, 90, 26)
	create("BUTTON", "Запустить "+w.request.Product, bsAutoCheckBox, idLaunch,
		bannerWidth+24, 240, winWidth-bannerWidth-48, 24)

	// Галочка «Запустить» по умолчанию стоит: человек только что поставил
	// программу, почти наверняка он хочет её открыть.
	pSendMessage.Call(uintptr(w.controls[idLaunch]), bmSetCheck, bstChecked, 0)
}

func (w *wizard) show(id uintptr, visible bool) {
	handle, ok := w.controls[id]
	if !ok || handle == 0 {
		return
	}
	state := uintptr(swHide)
	if visible {
		state = swShow
	}
	pShowWindow.Call(uintptr(handle), state)
}

func (w *wizard) enable(id uintptr, enabled bool) {
	handle, ok := w.controls[id]
	if !ok || handle == 0 {
		return
	}
	state := uintptr(0)
	if enabled {
		state = 1
	}
	pEnableWindow.Call(uintptr(handle), state)
}

func (w *wizard) setText(id uintptr, value string) {
	handle, ok := w.controls[id]
	if !ok || handle == 0 {
		return
	}
	pSetWindowText.Call(uintptr(handle), uintptr(unsafe.Pointer(utf16ptr(value))))
}

// applyPage расставляет элементы под текущую страницу.
func (w *wizard) applyPage() {
	w.mu.Lock()
	page := w.page
	failed := w.failed
	w.mu.Unlock()

	w.show(idPath, page == pageDir)
	w.show(idBrowse, page == pageDir)
	w.show(idLaunch, page == pageFinish && !failed)

	switch page {
	case pageWelcome:
		w.show(idBack, true)
		w.show(idNext, true)
		w.show(idCancel, true)
		w.enable(idBack, false)
		w.enable(idNext, true)
		w.enable(idCancel, true)
		w.setText(idNext, "Далее >")

	case pageDir:
		w.enable(idBack, true)
		w.enable(idNext, true)
		w.setText(idNext, "Далее >")

	case pageReady:
		w.enable(idBack, true)
		w.enable(idNext, true)
		w.setText(idNext, "Установить")

	case pageInstall:
		// Во время установки уйти некуда: файлы уже заменяются.
		w.enable(idBack, false)
		w.enable(idNext, false)
		w.enable(idCancel, false)
		w.setText(idNext, "Установить")

	case pageFinish:
		w.show(idBack, false)
		w.show(idCancel, false)
		w.enable(idNext, true)
		w.setText(idNext, "Завершить")
	}

	if page != pageInstall {
		pSetFocus.Call(uintptr(w.controls[idNext]))
	}
	pInvalidateRect.Call(uintptr(w.window), 0, 1)
}

func (w *wizard) pump() {
	var message msg
	for {
		result, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

// ---------------------------------------------------------------- сообщения

func windowProc(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
	w := active
	if w == nil {
		result, _, _ := pDefWindowProc.Call(window, uintptr(message), wParam, lParam)
		return result
	}

	switch message {
	case wmPaint:
		w.paint(window)
		return 0

	case wmCtlColorEdit:
		pSetTextColor.Call(wParam, colorInk)
		pSetBkMode.Call(wParam, transparent)
		return w.brushPage

	case wmCtlColorStatic, wmCtlColorBtn:
		// Галочка «Запустить» стоит на белой странице: без этого под ней
		// оставался серый прямоугольник.
		pSetTextColor.Call(wParam, colorInk)
		pSetBkMode.Call(wParam, transparent)
		return w.brushPage

	case wmCommand:
		w.command(uintptr(uint16(wParam)))
		return 0

	case wmProgress:
		w.repaintProgress()
		return 0

	case wmFinished:
		w.applyPage()
		return 0

	case wmClose:
		w.mu.Lock()
		busy := w.page == pageInstall
		w.mu.Unlock()
		if busy {
			return 0
		}
		pDestroyWindow.Call(window)
		return 0

	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}

	result, _, _ := pDefWindowProc.Call(window, uintptr(message), wParam, lParam)
	return result
}

func (w *wizard) command(id uintptr) {
	switch id {
	case idBrowse:
		if chosen := w.browse(); chosen != "" {
			pSetWindowText.Call(uintptr(w.pathEdit), uintptr(unsafe.Pointer(utf16ptr(chosen))))
		}

	case idBack:
		w.goBack()

	case idNext:
		w.goNext()

	case idCancel:
		pDestroyWindow.Call(uintptr(w.window))
	}
}

func (w *wizard) goBack() {
	w.mu.Lock()
	switch w.page {
	case pageDir:
		w.page = pageWelcome
	case pageReady:
		if w.request.AllowChooseDir {
			w.page = pageDir
		} else {
			w.page = pageWelcome
		}
	}
	w.mu.Unlock()
	w.applyPage()
}

func (w *wizard) goNext() {
	w.mu.Lock()
	page := w.page
	w.mu.Unlock()

	switch page {
	case pageWelcome:
		next := pageReady
		if w.request.AllowChooseDir {
			next = pageDir
		}
		w.mu.Lock()
		w.page = next
		w.mu.Unlock()
		w.applyPage()

	case pageDir:
		if problem := w.checkDir(); problem != "" {
			pMessageBox.Call(uintptr(w.window),
				uintptr(unsafe.Pointer(utf16ptr(problem))),
				uintptr(unsafe.Pointer(utf16ptr("Папка установки"))),
				mbOK|mbIconWarn)
			return
		}
		w.mu.Lock()
		w.page = pageReady
		w.mu.Unlock()
		w.applyPage()

	case pageReady:
		w.begin()

	case pageFinish:
		if handle, ok := w.controls[idLaunch]; ok && handle != 0 {
			checked, _, _ := pSendMessage.Call(uintptr(handle), bmGetCheck, 0, 0)
			w.outcome.Launch = checked == bstChecked && !w.failed
		}
		pDestroyWindow.Call(uintptr(w.window))
	}
}

func (w *wizard) selectedDir() string {
	if w.request.AllowChooseDir && w.pathEdit != 0 {
		if value := windowText(w.pathEdit); value != "" {
			return value
		}
	}
	return w.request.DefaultDir
}

func (w *wizard) checkDir() string {
	if w.request.ValidateDir == nil {
		return ""
	}
	return w.request.ValidateDir(w.selectedDir())
}

func (w *wizard) begin() {
	if w.started {
		return
	}
	directory := w.selectedDir()
	if problem := w.checkDir(); problem != "" {
		pMessageBox.Call(uintptr(w.window),
			uintptr(unsafe.Pointer(utf16ptr(problem))),
			uintptr(unsafe.Pointer(utf16ptr("Папка установки"))),
			mbOK|mbIconWarn)
		return
	}

	w.started = true
	w.outcome.Dir = directory

	w.mu.Lock()
	w.page = pageInstall
	w.percent = 0
	w.status = "Подготовка..."
	w.mu.Unlock()
	w.applyPage()

	go func() {
		warning, err := w.install(directory, w.report)

		w.mu.Lock()
		w.page = pageFinish
		w.failed = err != nil
		w.warning = warning
		if err != nil {
			w.failure = err.Error()
		} else {
			w.percent = 100
			w.outcome.Installed = true
		}
		w.mu.Unlock()

		pPostMessage.Call(uintptr(w.window), wmFinished, 0, 0)
	}()
}

func (w *wizard) report(percent int, text string) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	w.mu.Lock()
	w.percent = percent
	if text != "" {
		w.status = text
	}
	w.mu.Unlock()
	pPostMessage.Call(uintptr(w.window), wmProgress, 0, 0)
}

// repaintProgress перерисовывает только область страницы, а не всё окно:
// иначе кнопки внизу мигают на каждый шаг скачивания.
func (w *wizard) repaintProgress() {
	area := rect{
		Left:   w.dpi(0),
		Top:    w.dpi(headerHeight + 1),
		Right:  w.dpi(winWidth),
		Bottom: w.dpi(pageBottom),
	}
	pInvalidateRect.Call(uintptr(w.window), uintptr(unsafe.Pointer(&area)), 1)
}

func (w *wizard) browse() string {
	buffer := make([]uint16, 260)
	info := browseInfo{
		Owner:       uintptr(w.window),
		DisplayName: &buffer[0],
		Title:       utf16ptr("Куда установить " + w.request.Product),
		// BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE
		Flags: 0x0001 | 0x0040,
	}
	list, _, _ := pSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&info)))
	if list == 0 {
		return ""
	}
	defer pCoTaskMemFree.Call(list)

	path := make([]uint16, 520)
	if ok, _, _ := pSHGetPathFromIDList.Call(list, uintptr(unsafe.Pointer(&path[0]))); ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(path)
}

func windowText(handle hwnd) string {
	length, _, _ := pGetWindowTextLen.Call(uintptr(handle))
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, length+1)
	pGetWindowText.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), length+1)
	return syscall.UTF16ToString(buffer)
}

// ---------------------------------------------------------------- рисование

func (w *wizard) paint(window uintptr) {
	var ps paintStruct
	dc, _, _ := pBeginPaint.Call(window, uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(window, uintptr(unsafe.Pointer(&ps)))

	var client rect
	pGetClientRect.Call(window, uintptr(unsafe.Pointer(&client)))

	w.mu.Lock()
	page := w.page
	percent := w.percent
	status := w.status
	failed := w.failed
	failure := w.failure
	warning := w.warning
	w.mu.Unlock()

	// Страница и полоса с кнопками.
	pageArea := rect{Left: 0, Top: 0, Right: client.Right, Bottom: w.dpi(pageBottom)}
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&pageArea)), w.brushPage)
	bar := rect{Left: 0, Top: w.dpi(pageBottom), Right: client.Right, Bottom: client.Bottom}
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&bar)), w.brushBar)
	w.hline(dc, 0, pageBottom, winWidth, colorBarLine)

	pSetBkMode.Call(dc, transparent)

	banner := page == pageWelcome || page == pageFinish
	if banner {
		w.drawBanner(dc)
	}

	switch page {
	case pageWelcome:
		title := "Вас приветствует мастер установки " + w.request.Product
		if w.request.Upgrade {
			title = "Обновление " + w.request.Product
		}
		w.text(dc, title, w.fontBig, colorInk, bannerWidth+24, 28, winWidth-bannerWidth-48, 62, dtLeft|dtWordBreak|dtNoPrefix)

		body := "Эта программа установит " + w.request.Product + " версии " +
			w.request.Version + " на ваш компьютер.\n\n" +
			"Перед началом установки рекомендуется закрыть все работающие " +
			"приложения.\n\n" +
			"Нажмите «Далее» для продолжения."
		if w.request.Upgrade {
			body = "Установленная версия будет заменена на " + w.request.Version + ".\n\n" +
				"Подписки и настройки сохранятся: они хранятся отдельно от " +
				"файлов программы.\n\n" +
				"Нажмите «Далее» для продолжения."
		}
		w.text(dc, body, w.fontBody, colorInk, bannerWidth+24, 104, winWidth-bannerWidth-48, 200, dtLeft|dtWordBreak|dtNoPrefix)

	case pageDir:
		w.drawHeader(dc, "Выбор папки установки",
			"В какую папку установить "+w.request.Product+"?")
		w.text(dc, "Программа будет установлена в указанную ниже папку.",
			w.fontBody, colorInk, 24, 84, winWidth-48, 24, dtLeft|dtSingleLine|dtNoPrefix)
		w.text(dc, "Если нужна другая папка, нажмите «Обзор». Права администратора "+
			"не требуются — выбирайте папку внутри своего профиля.",
			w.fontBody, colorMuted, 24, 154, winWidth-48, 60, dtLeft|dtWordBreak|dtNoPrefix)

	case pageReady:
		w.drawHeader(dc, "Всё готово к установке",
			"Программа готова начать установку "+w.request.Product+".")
		body := "Папка установки:\n    " + w.selectedDir()
		if w.request.Hint != "" {
			body += "\n\n" + w.request.Hint
		}
		body += "\n\nНажмите «Установить», чтобы продолжить."
		w.text(dc, body, w.fontBody, colorInk, 24, 84, winWidth-48, 220, dtLeft|dtWordBreak|dtNoPrefix)

	case pageInstall:
		w.drawHeader(dc, "Установка",
			"Подождите, идёт установка "+w.request.Product+".")
		w.text(dc, status, w.fontBody, colorInk, 24, 128, winWidth-48, 24, dtLeft|dtSingleLine|dtEndEllipsis|dtNoPrefix)
		w.progressBar(dc, 24, 158, winWidth-48, 14, percent)
		w.text(dc, fmt.Sprintf("%d%%", percent), w.fontBody, colorMuted, 24, 180, winWidth-48, 22, dtLeft|dtSingleLine|dtNoPrefix)

	case pageFinish:
		if failed {
			w.text(dc, "Установка не завершена", w.fontBig, colorBad,
				bannerWidth+24, 28, winWidth-bannerWidth-48, 62, dtLeft|dtWordBreak|dtNoPrefix)
			w.text(dc, failure+"\n\nНажмите «Завершить», чтобы закрыть мастер.",
				w.fontBody, colorInk, bannerWidth+24, 104, winWidth-bannerWidth-48, 200, dtLeft|dtWordBreak|dtNoPrefix)
		} else {
			w.text(dc, "Установка завершена", w.fontBig, colorInk,
				bannerWidth+24, 28, winWidth-bannerWidth-48, 62, dtLeft|dtWordBreak|dtNoPrefix)
			body := w.request.Product + " установлен на ваш компьютер.\n\nПапка: " + w.outcome.Dir
			if warning == "" {
				body += "\n\nНажмите «Завершить», чтобы закрыть мастер."
			}
			w.text(dc, body, w.fontBody, colorInk,
				bannerWidth+24, 104, winWidth-bannerWidth-48, 110, dtLeft|dtWordBreak|dtNoPrefix)
			if warning != "" {
				w.text(dc, warning, w.fontBody, colorWarn,
					bannerWidth+24, 208, winWidth-bannerWidth-48, 80, dtLeft|dtWordBreak|dtNoPrefix)
			}
		}
	}
}

// drawHeader рисует шапку внутренних страниц: название сверху, пояснение
// под ним, значок справа и черта снизу.
func (w *wizard) drawHeader(dc uintptr, title, subtitle string) {
	w.text(dc, title, w.fontBold, colorInk, 24, 12, winWidth-100, 20, dtLeft|dtSingleLine|dtNoPrefix)
	w.text(dc, subtitle, w.fontBody, colorMuted, 36, 32, winWidth-116, 20, dtLeft|dtSingleLine|dtEndEllipsis|dtNoPrefix)
	if w.icon != 0 {
		pDrawIconEx.Call(dc, uintptr(w.dpi(winWidth-24-32)), uintptr(w.dpi(13)), w.icon,
			uintptr(w.dpi(32)), uintptr(w.dpi(32)), 0, 0, 3 /* DI_NORMAL */)
	}
	w.hline(dc, 0, headerHeight, winWidth, colorBarLine)
}

// drawBanner рисует левую колонку страниц приветствия и завершения.
func (w *wizard) drawBanner(dc uintptr) {
	w.gradient(dc, 0, 0, bannerWidth, pageBottom, colorBanner, colorBannerLow)

	if w.logo != nil {
		// Знак вписываем по ширине с полями, высоту считаем по пропорции.
		const target = 116
		height := target * w.logo.height / w.logo.width
		w.logo.draw(dc, w.dpi((bannerWidth-target)/2), w.dpi(40), w.dpi(target), w.dpi(height))
	}

	w.text(dc, w.request.Product, w.fontBold, 0xFFFFFF, 0, 116, bannerWidth, 22,
		dtCenter|dtSingleLine|dtNoPrefix)
	w.text(dc, "версия "+w.request.Version, w.fontBody, 0xB0A0A8, 0, 136, bannerWidth, 20,
		dtCenter|dtSingleLine|dtNoPrefix)

	bullets := w.request.Bullets
	if len(bullets) > 4 {
		bullets = bullets[:4]
	}
	top := pageBottom - 24 - len(bullets)*22
	for index, line := range bullets {
		y := top + index*22
		w.dot(dc, 16, y+7, 5, colorBullet)
		w.text(dc, line, w.fontBody, 0xE8DCE4, 30, y, bannerWidth-38, 20,
			dtLeft|dtSingleLine|dtEndEllipsis|dtNoPrefix)
	}
}

func (w *wizard) text(dc uintptr, value string, font uintptr, color uint32, x, y, width, height int, format uint32) {
	if value == "" {
		return
	}
	previous, _, _ := pSelectObject.Call(dc, font)
	pSetTextColor.Call(dc, uintptr(color))
	area := rect{
		Left:   w.dpi(x),
		Top:    w.dpi(y),
		Right:  w.dpi(x + width),
		Bottom: w.dpi(y + height),
	}
	runes := utf16ptr(value)
	pDrawText.Call(dc, uintptr(unsafe.Pointer(runes)), ^uintptr(0),
		uintptr(unsafe.Pointer(&area)), uintptr(format))
	pSelectObject.Call(dc, previous)
}

func (w *wizard) fill(dc uintptr, x, y, width, height int, color uint32) {
	brush := solidBrush(color)
	area := rect{Left: w.dpi(x), Top: w.dpi(y), Right: w.dpi(x + width), Bottom: w.dpi(y + height)}
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&area)), brush)
	pDeleteObject.Call(brush)
}

func (w *wizard) hline(dc uintptr, x, y, width int, color uint32) {
	brush := solidBrush(color)
	area := rect{Left: w.dpi(x), Top: w.dpi(y), Right: w.dpi(x + width), Bottom: w.dpi(y) + 1}
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&area)), brush)
	pDeleteObject.Call(brush)
}

func (w *wizard) dot(dc uintptr, x, y, radius int, color uint32) {
	brush := solidBrush(color)
	pen, _, _ := pCreatePen.Call(psSolid, 1, uintptr(color))
	oldBrush, _, _ := pSelectObject.Call(dc, brush)
	oldPen, _, _ := pSelectObject.Call(dc, pen)
	pEllipse.Call(dc,
		uintptr(w.dpi(x-radius)), uintptr(w.dpi(y-radius)),
		uintptr(w.dpi(x+radius)), uintptr(w.dpi(y+radius)))
	pSelectObject.Call(dc, oldBrush)
	pSelectObject.Call(dc, oldPen)
	pDeleteObject.Call(brush)
	pDeleteObject.Call(pen)
}

// gradient заливает прямоугольник сверху вниз. GradientFill живёт в
// msimg32.dll; если её нет, просто заливаем верхним цветом.
func (w *wizard) gradient(dc uintptr, x, y, width, height int, from, to uint32) {
	if err := msimg32.Load(); err != nil {
		w.fill(dc, x, y, width, height, from)
		return
	}
	split := func(color uint32) (uint16, uint16, uint16) {
		// COLORREF хранит цвет как 0x00BBGGRR.
		return uint16(color&0xFF) << 8, uint16((color>>8)&0xFF) << 8, uint16((color>>16)&0xFF) << 8
	}
	r1, g1, b1 := split(from)
	r2, g2, b2 := split(to)
	vertices := [2]trivertex{
		{X: w.dpi(x), Y: w.dpi(y), Red: r1, Green: g1, Blue: b1, Alpha: 0},
		{X: w.dpi(x + width), Y: w.dpi(y + height), Red: r2, Green: g2, Blue: b2, Alpha: 0},
	}
	area := gradientRect{UpperLeft: 0, LowerRight: 1}
	const gradientFillRectV = 1
	pAlphaSafe, _, _ := pGradientFill.Call(dc,
		uintptr(unsafe.Pointer(&vertices[0])), 2,
		uintptr(unsafe.Pointer(&area)), 1, gradientFillRectV)
	if pAlphaSafe == 0 {
		w.fill(dc, x, y, width, height, from)
	}
}

func (w *wizard) progressBar(dc uintptr, x, y, width, height, percent int) {
	w.fill(dc, x, y, width, height, colorTrack)
	filled := width * percent / 100
	if filled > 0 {
		w.fill(dc, x, y, filled, height, colorAccent)
	}
	// Тонкая рамка, чтобы дорожка читалась на белом.
	w.hline(dc, x, y, width, colorBarLine)
	w.hline(dc, x, y+height, width, colorBarLine)
}

// ---------------------------------------------------------------- картинки

// bitmap — распакованный PNG, готовый к выводу с прозрачностью.
type bitmap struct {
	handle uintptr
	width  int
	height int
}

// loadPNG раскладывает PNG в 32-битный DIB с предумноженной альфой:
// именно такой ждёт AlphaBlend. Ошибка не страшна — баннер просто
// останется без знака.
func loadPNG(data []byte) *bitmap {
	if len(data) == 0 {
		return nil
	}
	source, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil
	}

	// image/draw переводит в RGBA с предумноженной альфой сам.
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(rgba, rgba.Bounds(), source, bounds.Min, draw.Src)

	header := bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(width),
		Height:      -int32(height), // сверху вниз
		Planes:      1,
		BitCount:    32,
		Compression: 0, // BI_RGB
	}
	var pixels unsafe.Pointer
	handle, _, _ := pCreateDIBSection.Call(0,
		uintptr(unsafe.Pointer(&header)), 0, /* DIB_RGB_COLORS */
		uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if handle == 0 || pixels == nil {
		return nil
	}

	destination := unsafe.Slice((*byte)(pixels), width*height*4)
	for index := 0; index < width*height; index++ {
		r := rgba.Pix[index*4+0]
		g := rgba.Pix[index*4+1]
		b := rgba.Pix[index*4+2]
		a := rgba.Pix[index*4+3]
		// В DIB порядок байт BGRA.
		destination[index*4+0] = b
		destination[index*4+1] = g
		destination[index*4+2] = r
		destination[index*4+3] = a
	}
	return &bitmap{handle: handle, width: width, height: height}
}

func (b *bitmap) draw(dc uintptr, x, y, width, height int32) {
	if b == nil || b.handle == 0 {
		return
	}
	memory, _, _ := pCreateCompatibleDC.Call(dc)
	if memory == 0 {
		return
	}
	defer pDeleteDC.Call(memory)
	previous, _, _ := pSelectObject.Call(memory, b.handle)
	defer pSelectObject.Call(memory, previous)

	// BLENDFUNCTION{AC_SRC_OVER, 0, 255, AC_SRC_ALPHA} одним словом.
	const blend = 0x01FF0000
	pAlphaBlend.Call(dc, uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		memory, 0, 0, uintptr(b.width), uintptr(b.height), blend)
}
