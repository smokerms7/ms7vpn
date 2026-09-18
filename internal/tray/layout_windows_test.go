//go:build windows

package tray

import (
	"testing"
	"unsafe"
)

// Структуры Win32 описаны в Go вручную. Если раскладка полей разъедется,
// Shell_NotifyIcon начнёт получать мусор, а значок либо не появится, либо
// появится с испорченной подсказкой. Проверяем размеры и смещения явно.
func TestNotifyIconDataLayout(t *testing.T) {
	var data notifyIconData
	if size := unsafe.Sizeof(data); size != 976 {
		t.Fatalf("sizeof(NOTIFYICONDATAW) = %d, ожидается 976", size)
	}
	offsets := []struct {
		name   string
		actual uintptr
		want   uintptr
	}{
		{"hWnd", unsafe.Offsetof(data.HWnd), 8},
		{"uID", unsafe.Offsetof(data.UID), 16},
		{"hIcon", unsafe.Offsetof(data.HIcon), 32},
		{"szTip", unsafe.Offsetof(data.SzTip), 40},
		{"dwState", unsafe.Offsetof(data.DwState), 296},
		{"szInfo", unsafe.Offsetof(data.SzInfo), 304},
		{"uVersion", unsafe.Offsetof(data.UVersion), 816},
		{"szInfoTitle", unsafe.Offsetof(data.SzInfoTitle), 820},
		{"dwInfoFlags", unsafe.Offsetof(data.DwInfoFlags), 948},
		{"guidItem", unsafe.Offsetof(data.GuidItem), 952},
		{"hBalloonIcon", unsafe.Offsetof(data.HBalloonIcon), 968},
	}
	for _, item := range offsets {
		if item.actual != item.want {
			t.Errorf("смещение %s = %d, ожидается %d", item.name, item.actual, item.want)
		}
	}
}

func TestWndClassExLayout(t *testing.T) {
	var class wndClassEx
	if size := unsafe.Sizeof(class); size != 80 {
		t.Fatalf("sizeof(WNDCLASSEXW) = %d, ожидается 80", size)
	}
}

func TestCopyUTF16Truncates(t *testing.T) {
	var buffer [8]uint16
	copyUTF16(buffer[:], "очень длинная подсказка")
	if buffer[len(buffer)-1] != 0 {
		t.Fatal("строка должна оставаться нуль-терминированной после обрезки")
	}
	copyUTF16(buffer[:], "ок")
	if buffer[2] != 0 {
		t.Fatal("хвост буфера должен обнуляться между вызовами")
	}
}
