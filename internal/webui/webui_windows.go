//go:build windows

// Package webui показывает интерфейс MS7VPN в окне WebView2 (Edge Chromium).
// Логика приложения не меняется: окно ходит в тот же локальный HTTP API,
// что и раньше, но страница рисуется на HTML и CSS в фирменном стиле MS7.
package webui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	ms7app "ms7vpn/internal/app"
)

// deviceInfo — данные для раздела «Информация».
type deviceInfo struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	OSName     string `json:"osName"`
}

func collectDeviceInfo(dataDir string) deviceInfo {
	info := deviceInfo{}
	if data, err := os.ReadFile(filepath.Join(dataDir, "device.id")); err == nil {
		info.DeviceID = strings.TrimSpace(string(data))
	}
	if name, err := os.Hostname(); err == nil {
		info.DeviceName = name
	}
	version := windows.RtlGetVersion()
	if version != nil {
		edition := "Windows"
		if version.MajorVersion >= 10 && version.BuildNumber >= 22000 {
			edition = "Windows 11"
		} else if version.MajorVersion >= 10 {
			edition = "Windows 10"
		}
		info.OSName = fmt.Sprintf("%s (сборка %d)", edition, version.BuildNumber)
	}
	return info
}

//go:embed assets/*
var assetsFS embed.FS

//go:embed icon/MS7VPN.ico
var windowIcon []byte

// enableDarkTitleBar красит системную полосу окна в тёмный цвет,
// чтобы она не была белой на фоне тёмного интерфейса.
func enableDarkTitleBar(handle uintptr) {
	if handle == 0 {
		return
	}
	dwm := windows.NewLazySystemDLL("dwmapi.dll")
	setAttribute := dwm.NewProc("DwmSetWindowAttribute")
	enabled := int32(1)
	const (
		useImmersiveDarkMode    = 20
		useImmersiveDarkModePre = 19
		captionColor            = 35
		textColor               = 36
		borderColor             = 34
	)
	setAttribute.Call(handle, useImmersiveDarkMode, uintptr(unsafe.Pointer(&enabled)), 4)
	setAttribute.Call(handle, useImmersiveDarkModePre, uintptr(unsafe.Pointer(&enabled)), 4)
	// Windows 11: цвет полосы и текста в тон интерфейса (COLORREF = 0x00BBGGRR).
	caption := int32(0x000D0A16)
	text := int32(0x00F8F0F3)
	border := int32(0x00331D24)
	setAttribute.Call(handle, captionColor, uintptr(unsafe.Pointer(&caption)), 4)
	setAttribute.Call(handle, textColor, uintptr(unsafe.Pointer(&text)), 4)
	setAttribute.Call(handle, borderColor, uintptr(unsafe.Pointer(&border)), 4)
}

// applyWindowIcon ставит фирменную иконку окну и панели задач.
func applyWindowIcon(handle uintptr, dataDir string) {
	if handle == 0 || len(windowIcon) == 0 {
		return
	}
	path := filepath.Join(dataDir, "ms7vpn.ico")
	if err := os.WriteFile(path, windowIcon, 0o600); err != nil {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	loadImage := user32.NewProc("LoadImageW")
	sendMessage := user32.NewProc("SendMessageW")
	namePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	const (
		imageIcon      = 1
		loadFromFile   = 0x00000010
		wmSetIcon      = 0x0080
		iconBig        = 1
		iconSmall      = 0
	)
	big, _, _ := loadImage.Call(0, uintptr(unsafe.Pointer(namePtr)), imageIcon, 64, 64, loadFromFile)
	small, _, _ := loadImage.Call(0, uintptr(unsafe.Pointer(namePtr)), imageIcon, 16, 16, loadFromFile)
	if big != 0 {
		sendMessage.Call(handle, wmSetIcon, iconBig, big)
	}
	if small != 0 {
		sendMessage.Call(handle, wmSetIcon, iconSmall, small)
	}
}

const (
	windowTitle  = "MS7VPN"
	windowWidth  = 1040
	windowHeight = 720
	minWidth     = 900
	minHeight    = 640
)

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Run поднимает локальный сервер на 127.0.0.1 и открывает окно WebView2.
// Возвращает ошибку, если WebView2 недоступен — вызывающий код может
// откатиться на старое нативное окно.
func Run(app *ms7app.App, autoConnect string) error {
	token, err := newToken()
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	static, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		listener.Close()
		return err
	}

	mux := http.NewServeMux()
	app.RegisterRoutes(mux, token)
	mux.HandleFunc("/api/device", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-MS7-Token") != token && r.URL.Query().Get("token") != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": collectDeviceInfo(app.DataDir())})
	})
	var pageLoads atomic.Int32
	fileServer := http.FileServer(http.FS(static))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Окно открывается только с локального адреса и только этим процессом.
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			page, readErr := fs.ReadFile(static, "index.html")
			if readErr != nil {
				http.Error(w, "UI missing", http.StatusInternalServerError)
				return
			}
			body := strings.ReplaceAll(string(page), "__MS7_TOKEN__", token)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(body))
			pageLoads.Add(1)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			fmt.Println("MS7VPN local server:", serveErr)
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	view := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  windowTitle,
			Width:  windowWidth,
			Height: windowHeight,
			IconId: 1, // идентификатор группы значков в rsrc_windows_amd64.syso
			Center: true,
		},
	})
	if view == nil {
		return errors.New("WebView2 недоступен")
	}
	defer view.Destroy()

	applyWindowIcon(uintptr(view.Window()), app.DataDir())
	enableDarkTitleBar(uintptr(view.Window()))
	view.SetSize(minWidth, minHeight, webview2.HintMin)
	view.SetSize(windowWidth, windowHeight, webview2.HintNone)

	stopTray := startTray(app, uintptr(view.Window()))
	defer stopTray()

	url := fmt.Sprintf("http://%s/?token=%s", listener.Addr().String(), token)
	if autoConnect != "" {
		url += "&autoconnect=" + autoConnect
	}
	view.Navigate(url)

	// WebView2 иногда не подхватывает первую навигацию — окно остаётся белым.
	// Если страница так и не была запрошена, повторяем переход.
	go func() {
		for attempt := 0; attempt < 3; attempt++ {
			time.Sleep(1500 * time.Millisecond)
			if pageLoads.Load() > 0 {
				return
			}
			view.Dispatch(func() { view.Navigate(url) })
		}
	}()

	go func() {
		<-app.Done()
		view.Dispatch(func() { view.Terminate() })
	}()

	view.Run()
	return nil
}
