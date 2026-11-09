//go:build windows

package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Иконки программ для «Без VPN»: находим exe по имени и достаём из него
// значок. Ищем по запущенным процессам, по App Paths в реестре и по обычным
// папкам установки. Результаты кешируются: обход папок не быстрый.

var (
	shell32           = windows.NewLazySystemDLL("shell32.dll")
	user32Icons       = windows.NewLazySystemDLL("user32.dll")
	gdi32             = windows.NewLazySystemDLL("gdi32.dll")
	procExtractIconEx = shell32.NewProc("ExtractIconExW")
	procDestroyIcon   = user32Icons.NewProc("DestroyIcon")
	procGetIconInfo   = user32Icons.NewProc("GetIconInfo")
	procGetDC         = user32Icons.NewProc("GetDC")
	procReleaseDC     = user32Icons.NewProc("ReleaseDC")
	procGetDIBits     = gdi32.NewProc("GetDIBits")
	procGetObject     = gdi32.NewProc("GetObjectW")
	procDeleteObject  = gdi32.NewProc("DeleteObject")
	iconCache         sync.Map // имя → []byte (nil — не нашли)
	exeIndexMu        sync.Mutex
	exeIndex          map[string]string
	exeIndexBuilt     time.Time
)

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

type bitmapHeader struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

// appIconPNG — значок программы в PNG или nil.
func appIconPNG(name string) []byte {
	key := strings.ToLower(strings.TrimSpace(name))
	if cached, ok := iconCache.Load(key); ok {
		data, _ := cached.([]byte)
		return data
	}
	var data []byte
	if path := findProgramPath(key); path != "" {
		data = extractIconPNG(path)
	}
	iconCache.Store(key, data)
	return data
}

func findProgramPath(name string) string {
	if strings.ContainsAny(name, `\/`) {
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			return name
		}
		name = strings.ToLower(filepath.Base(name))
	}
	if path := runningProgramPath(name); path != "" {
		return path
	}
	if path := appPathsLookup(name); path != "" {
		return path
	}
	if name == "steam.exe" {
		if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam`, registry.QUERY_VALUE); err == nil {
			value, _, _ := key.GetStringValue("SteamExe")
			key.Close()
			if value != "" {
				return filepath.FromSlash(value)
			}
		}
	}
	return installedExeIndex()[name]
}

func runningProgramPath(name string) string {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), name) {
			continue
		}
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if err != nil {
			continue
		}
		buffer := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buffer))
		err = windows.QueryFullProcessImageName(process, 0, &buffer[0], &size)
		windows.CloseHandle(process)
		if err == nil {
			return windows.UTF16ToString(buffer[:size])
		}
	}
	return ""
}

func appPathsLookup(name string) string {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		key, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		value, _, _ := key.GetStringValue("")
		key.Close()
		value = strings.Trim(value, `" `)
		if value != "" {
			if info, err := os.Stat(value); err == nil && !info.IsDir() {
				return value
			}
		}
	}
	return ""
}

// installedExeIndex — exe из обычных папок установки, на глубину до 4
// уровней. Строится раз в 10 минут.
func installedExeIndex() map[string]string {
	exeIndexMu.Lock()
	defer exeIndexMu.Unlock()
	if exeIndex != nil && time.Since(exeIndexBuilt) < 10*time.Minute {
		return exeIndex
	}
	index := map[string]string{}
	roots := []string{}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if dir := os.Getenv(env); dir != "" {
			roots = append(roots, dir)
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "Programs"), local)
	}
	if roaming := os.Getenv("APPDATA"); roaming != "" {
		roots = append(roots, roaming)
	}
	visited := 0
	for _, root := range roots {
		rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			visited++
			if visited > 120000 {
				return filepath.SkipAll
			}
			depth := strings.Count(path, string(filepath.Separator)) - rootDepth
			if entry.IsDir() {
				lower := strings.ToLower(entry.Name())
				if depth > 4 || lower == "windowsapps" || lower == "packages" || lower == "temp" || lower == "cache" || strings.HasPrefix(lower, "microsoft") {
					return filepath.SkipDir
				}
				return nil
			}
			if lower := strings.ToLower(entry.Name()); strings.HasSuffix(lower, ".exe") {
				if _, seen := index[lower]; !seen {
					index[lower] = path
				}
			}
			return nil
		})
	}
	exeIndex = index
	exeIndexBuilt = time.Now()
	return index
}

func extractIconPNG(path string) []byte {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	var large windows.Handle
	count, _, _ := procExtractIconEx.Call(uintptr(unsafe.Pointer(pathPtr)), 0, uintptr(unsafe.Pointer(&large)), 0, 1)
	if count == 0 || large == 0 {
		return nil
	}
	defer procDestroyIcon.Call(uintptr(large))

	var info iconInfo
	if ok, _, _ := procGetIconInfo.Call(uintptr(large), uintptr(unsafe.Pointer(&info))); ok == 0 {
		return nil
	}
	defer procDeleteObject.Call(uintptr(info.hbmColor))
	defer procDeleteObject.Call(uintptr(info.hbmMask))
	if info.hbmColor == 0 {
		return nil
	}
	var bm bitmapHeader
	procGetObject.Call(uintptr(info.hbmColor), unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
	width, height := int(bm.bmWidth), int(bm.bmHeight)
	if width <= 0 || height <= 0 || width > 256 || height > 256 {
		return nil
	}
	dc, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, dc)

	read := func(bitmap windows.Handle) []byte {
		header := bitmapInfoHeader{biSize: 40, biWidth: int32(width), biHeight: -int32(height), biPlanes: 1, biBitCount: 32}
		buffer := make([]byte, width*height*4)
		lines, _, _ := procGetDIBits.Call(dc, uintptr(bitmap), 0, uintptr(height), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&header)), 0)
		if lines == 0 {
			return nil
		}
		return buffer
	}
	pixels := read(info.hbmColor)
	if pixels == nil {
		return nil
	}
	hasAlpha := false
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			hasAlpha = true
			break
		}
	}
	var mask []byte
	if !hasAlpha && info.hbmMask != 0 {
		mask = read(info.hbmMask)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			alpha := pixels[i+3]
			if !hasAlpha {
				alpha = 255
				if mask != nil && mask[i] != 0 {
					alpha = 0
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{R: pixels[i+2], G: pixels[i+1], B: pixels[i], A: alpha})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil
	}
	return out.Bytes()
}
