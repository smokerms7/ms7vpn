//go:build windows

// Package singleton не даёт запустить вторую копию MS7VPN.
//
// Без этого повторный запуск поднимал ещё одно окно и ещё один экземпляр
// WebView2 поверх той же папки профиля. Второе окно оставалось белым, а обе
// копии дрались за сетевой адаптер и локальные порты.
package singleton

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const mutexName = `Local\MS7VPN-single-instance`

// showMessageName — broadcast-сообщение «покажи окно». Имя регистрируется
// системой, поэтому обе копии получают один и тот же идентификатор.
const showMessageName = "MS7VPN.ShowWindow"

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procRegisterWindowMsg   = user32.NewProc("RegisterWindowMessageW")
	procPostMessage         = user32.NewProc("PostMessageW")
	procAllowSetForeground  = user32.NewProc("AllowSetForegroundWindow")
	handle                  windows.Handle
	cachedShowMessage       uint32
	cachedShowMessageLoaded bool
)

// ShowMessage возвращает идентификатор сообщения «показать окно».
func ShowMessage() uint32 {
	if cachedShowMessageLoaded {
		return cachedShowMessage
	}
	name, err := windows.UTF16PtrFromString(showMessageName)
	if err != nil {
		return 0
	}
	value, _, _ := procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(name)))
	cachedShowMessage = uint32(value)
	cachedShowMessageLoaded = true
	return cachedShowMessage
}

// Acquire пытается занять единственное место запуска.
//
// Возвращает false, если копия уже работает: в этом случае вызывающий код
// должен показать её окно и завершиться. wait задаёт, сколько ждать
// освобождения — это нужно при перезапуске с правами администратора, когда
// старая копия ещё не успела закрыться.
func Acquire(wait time.Duration) bool {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return true // не смогли проверить — не мешаем запуску
	}
	deadline := time.Now().Add(wait)
	for {
		created, err := windows.CreateMutex(nil, false, name)
		if err == nil {
			handle = created
			return true
		}
		if err != windows.ERROR_ALREADY_EXISTS {
			return true
		}
		if created != 0 {
			_ = windows.CloseHandle(created)
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// Release освобождает место запуска.
func Release() {
	if handle != 0 {
		_ = windows.CloseHandle(handle)
		handle = 0
	}
}

// SignalExisting просит уже запущенную копию показать своё окно.
func SignalExisting() {
	message := ShowMessage()
	if message == 0 {
		return
	}
	const asfwAny = ^uint32(0) // ASFW_ANY: разрешаем любой копии выйти на передний план
	procAllowSetForeground.Call(uintptr(asfwAny))
	const hwndBroadcast = 0xffff
	procPostMessage.Call(hwndBroadcast, uintptr(message), 0, 0)
}
