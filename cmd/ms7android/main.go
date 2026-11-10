//go:build ms7android

// ms7android — тот же MS7VPN, что и на Windows, только для телефона.
//
// Процесс запускает оболочка Android (Kotlin) и показывает интерфейс в WebView.
// Логика — общий код пакета internal/app: подписки, серверы, пинг, «Без VPN»,
// статистика, настройки. Отличия платформы — в internal/app/android_ms7android.go.
//
// Переменные окружения от оболочки:
//
//	MS7_DATA     папка данных
//	MS7_PORT     порт локального API
//	MS7_TOKEN    токен API
//	MS7_CORE     путь к исполняемому Xray (libxray.so)
//	MS7_GEO      папка с geoip.dat/geosite.dat
//	MS7_BRIDGE   имя сокета оболочки
//	MS7_DNS      DNS-серверы сети через запятую
//	MS7_VERSION_CODE, MS7_PACKAGE, MS7_DEVICE, MS7_OS, MS7_DEVICE_ID
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	ms7app "ms7vpn/internal/app"
	"ms7vpn/internal/model"
	"ms7vpn/internal/subscription"
	"ms7vpn/internal/update"
	"ms7vpn/internal/webui"
	"ms7vpn/internal/xray"
)

const updateManifestURL = "https://vpn.ms7pc.shop/api/downloads/android-latest.json"

func main() {
	dataDir := os.Getenv("MS7_DATA")
	port := os.Getenv("MS7_PORT")
	token := os.Getenv("MS7_TOKEN")
	if dataDir == "" || port == "" || token == "" {
		fmt.Println("MS7VPN for Android: запускается только из приложения")
		os.Exit(2)
	}
	_ = os.MkdirAll(dataDir, 0o700)
	if file, err := os.OpenFile(filepath.Join(dataDir, "ms7vpn.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(io.MultiWriter(file, os.Stderr))
	}

	setupResolver(os.Getenv("MS7_DNS"))
	// Серверу подписки — что это телефон, а не Windows.
	subscription.DeviceOS = "Android"
	subscription.DeviceOSVersion = strings.TrimSpace(os.Getenv("MS7_OS"))
	subscription.DeviceModel = strings.TrimSpace(os.Getenv("MS7_DEVICE"))
	if subscription.DeviceModel == "" {
		subscription.DeviceModel = "MS7VPN Android"
	}
	xray.CoreBinaryOverride = os.Getenv("MS7_CORE")
	xray.CoreDirOverride = os.Getenv("MS7_GEO")
	_ = os.Setenv("XRAY_LOCATION_ASSET", xray.CoreDirOverride)
	_ = os.Setenv("xray.location.asset", xray.CoreDirOverride)

	application, err := ms7app.New(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	forceAndroidSettings(application)
	application.StartBackgroundTasks()
	// «Отправлять HWID» меняется в настройках на лету.
	go func() {
		for {
			subscription.SendHWID = !application.Snapshot().Settings.NoHWID
			time.Sleep(time.Second)
		}
	}()
	// «Обновлять подписки при запуске», как в Happ.
	if application.Snapshot().Settings.RefreshOnStart {
		go func() {
			for _, sub := range application.Snapshot().Subscriptions {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				_, _ = application.RefreshSubscription(ctx, sub.ID)
				cancel()
			}
		}()
	}

	// Оболочка держит stdin открытым. Закрылся — приложение убито: гасим
	// Xray и выходим, чтобы не осталось «сиротских» процессов.
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		application.Shutdown()
		os.Exit(0)
	}()

	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: newHandler(application, token, dataDir), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("MS7VPN %s для Android: API на %s", model.AppVersion, listener.Addr())
	go func() {
		<-application.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// setupResolver: у Android нет /etc/resolv.conf, и чистый Go-резолвер
// стучался бы в 127.0.0.1:53. Берём DNS сети от оболочки, запасные — публичные.
func setupResolver(list string) {
	var servers []string
	for _, item := range strings.Split(list, ",") {
		if ip := net.ParseIP(strings.TrimSpace(item)); ip != nil {
			servers = append(servers, net.JoinHostPort(ip.String(), "53"))
		}
	}
	servers = append(servers, "1.1.1.1:53", "8.8.8.8:53", "77.88.8.8:53")
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var lastErr error
			for _, server := range servers {
				dialer := net.Dialer{Timeout: 3 * time.Second}
				conn, err := dialer.DialContext(ctx, network, server)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

// forceAndroidSettings: на телефоне одно ядро (Xray) и один режим — VPN на
// всё устройство через VpnService.
func forceAndroidSettings(application *ms7app.App) {
	settings := application.Snapshot().Settings
	changed := false
	if settings.Engine != "xray" {
		settings.Engine, changed = "xray", true
	}
	if settings.ConnectionMode != "tun" {
		settings.ConnectionMode, changed = "tun", true
	}
	if settings.Mode == model.ModeZapret {
		settings.Mode, changed = "vpn", true
	}
	if settings.ConsoleProxy {
		settings.ConsoleProxy, changed = false, true
	}
	if changed {
		_, _ = application.SaveSettings(settings)
	}
}

// fastestNode проверяет пинг всех серверов и возвращает самый быстрый.
func fastestNode(parent context.Context, application *ms7app.App) string {
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	state, err := application.PingNodes(ctx, "", nil)
	if err != nil {
		return ""
	}
	best, bestPing := "", 0
	for _, sub := range state.Subscriptions {
		for _, node := range sub.Nodes {
			if node.Supported && node.PingMS > 0 && (bestPing == 0 || node.PingMS < bestPing) {
				best, bestPing = node.ID, node.PingMS
			}
		}
	}
	if best != "" {
		_, _ = application.SelectNode(best)
	}
	return best
}

func localHost(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "127.0.0.1" || host == "localhost"
}

func authorized(r *http.Request, token string) bool {
	return localHost(r) && (r.Header.Get("X-MS7-Token") == token || r.URL.Query().Get("token") == token)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ok(w http.ResponseWriter, data any) { writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data}) }

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
}

func newHandler(application *ms7app.App, token, dataDir string) http.Handler {
	static, err := fs.Sub(webui.Assets, "assets")
	if err != nil {
		log.Fatal(err)
	}
	mobile, err := fs.Sub(webui.Assets, "mobile")
	if err != nil {
		log.Fatal(err)
	}
	inner := http.NewServeMux()
	application.RegisterRoutes(inner, token)

	updates := &androidUpdater{app: application}
	mux := http.NewServeMux()
	guard := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !authorized(r, token) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			handler(w, r)
		}
	}
	mux.HandleFunc("/api/device", guard(func(w http.ResponseWriter, r *http.Request) {
		deviceID := os.Getenv("MS7_DEVICE_ID")
		if data, err := os.ReadFile(filepath.Join(dataDir, "device.id")); err == nil {
			deviceID = strings.TrimSpace(string(data))
		}
		ok(w, map[string]string{
			"deviceId":   deviceID,
			"deviceName": os.Getenv("MS7_DEVICE"),
			"osName":     os.Getenv("MS7_OS"),
		})
	}))
	mux.HandleFunc("/api/ui-ready", guard(func(w http.ResponseWriter, r *http.Request) { ok(w, true) }))
	mux.HandleFunc("/api/processes", guard(func(w http.ResponseWriter, r *http.Request) {
		apps := ms7app.InstalledApps()
		names := make([]string, 0, len(apps))
		labels := map[string]string{}
		for _, app := range apps {
			names = append(names, app.Package)
			labels[app.Package] = app.Label
		}
		ok(w, map[string]any{"processes": names, "labels": labels})
	}))
	// Сервер для «Подключаться при запуске» — оболочка добавляет его в адрес
	// окна (autoconnect=…), как это делает окно на Windows.
	mux.HandleFunc("/api/android/autoconnect", guard(func(w http.ResponseWriter, r *http.Request) {
		target := ""
		if !application.Snapshot().Connection.Connected {
			target = application.AutoConnectNodeID()
			// Автоподключение к самому быстрому серверу, как в Happ.
			if target != "" && application.Snapshot().Settings.AutoConnectType == "fastest" {
				if best := fastestNode(r.Context(), application); best != "" {
					target = best
				}
			}
		}
		ok(w, map[string]string{"nodeId": target})
	}))
	mux.HandleFunc("/api/android/link", guard(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&request)
		_ = ms7app.SavePendingLink(dataDir, request.URL)
		ok(w, true)
	}))
	mux.HandleFunc("/api/update/check", guard(updates.handleCheck))
	mux.HandleFunc("/api/update/install", guard(updates.handleInstall))
	mux.HandleFunc("/api/update/progress", guard(updates.handleProgress))
	mux.HandleFunc("/api/open-data-folder", guard(func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusBadRequest, "UNSUPPORTED", "На Android папка с данными закрыта системой")
	}))
	mux.HandleFunc("/api/elevate", guard(func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusBadRequest, "UNSUPPORTED", "На Android права администратора не нужны")
	}))
	mux.Handle("/api/", inner)

	mobileFiles := http.StripPrefix("/m/", http.FileServer(http.FS(mobile)))
	files := http.FileServer(http.FS(static))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			if !authorized(r, token) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			page, err := fs.ReadFile(static, "index.html")
			if err != nil {
				http.Error(w, "UI missing", http.StatusInternalServerError)
				return
			}
			html := string(page)
			html = strings.Replace(html, `<meta name="viewport" content="width=device-width, initial-scale=1">`,
				`<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no, viewport-fit=cover">`, 1)
			html = strings.Replace(html, `<link rel="stylesheet" href="styles.css">`,
				`<link rel="stylesheet" href="styles.css"><link rel="stylesheet" href="m/mobile.css">`, 1)
			html = strings.Replace(html, `<script src="app.js"></script>`,
				`<script src="m/i18n.js"></script><script src="m/android.js"></script><script src="app.js"></script><script src="m/android-after.js"></script>`, 1)
			html = strings.Replace(html, "<body>", `<body class="is-android">`, 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(html))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/m/") {
			mobileFiles.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	return mux
}

// ==== обновления APK ====

type androidManifest struct {
	ApplicationID string `json:"applicationId"`
	VersionCode   int    `json:"versionCode"`
	VersionName   string `json:"versionName"`
	APKURL        string `json:"apkUrl"`
	SHA256        string `json:"sha256"`
	ReleaseNotes  string `json:"releaseNotes"`
}

type androidUpdater struct {
	app      *ms7app.App
	mu       sync.Mutex
	running  bool
	progress map[string]any
}

func (u *androidUpdater) fetch(ctx context.Context) (androidManifest, bool, error) {
	var manifest androidManifest
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, updateManifestURL, nil)
	request.Header.Set("Cache-Control", "no-cache")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return manifest, false, fmt.Errorf("сервер обновлений недоступен: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return manifest, false, fmt.Errorf("сервер обновлений ответил %s", response.Status)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&manifest); err != nil {
		return manifest, false, errors.New("не удалось разобрать ответ сервера обновлений")
	}
	current, _ := strconv.Atoi(os.Getenv("MS7_VERSION_CODE"))
	pkg := os.Getenv("MS7_PACKAGE")
	has := manifest.VersionCode > current && (manifest.ApplicationID == "" || manifest.ApplicationID == pkg) &&
		strings.HasPrefix(manifest.APKURL, "https://")
	return manifest, has, nil
}

func (u *androidUpdater) handleCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	manifest, has, err := u.fetch(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, "UPDATE_CHECK_FAILED", err.Error())
		return
	}
	result := map[string]any{"current": model.AppVersion, "hasUpdate": has, "checkedAt": time.Now().Unix()}
	if has {
		result["latest"] = manifest.VersionName
		result["downloadUrl"] = manifest.APKURL
		result["sha256"] = manifest.SHA256
		result["notes"] = manifest.ReleaseNotes
	} else {
		result["latest"] = model.AppVersion
	}
	ok(w, result)
}

func (u *androidUpdater) set(stage string, percent int, message, errText string) {
	u.mu.Lock()
	u.progress = map[string]any{"stage": stage, "percent": percent, "message": message}
	if errText != "" {
		u.progress["error"] = errText
	}
	u.mu.Unlock()
}

func (u *androidUpdater) handleProgress(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	progress := u.progress
	u.mu.Unlock()
	if progress == nil {
		progress = map[string]any{"stage": "idle"}
	}
	ok(w, progress)
}

func (u *androidUpdater) handleInstall(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		ok(w, map[string]bool{"started": true})
		return
	}
	u.running = true
	u.mu.Unlock()
	u.set("checking", 0, "Проверяем обновление…", "")
	go func() {
		defer func() { u.mu.Lock(); u.running = false; u.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		manifest, has, err := u.fetch(ctx)
		if err != nil || !has {
			if err == nil {
				err = errors.New("обновлений нет")
			}
			u.set("failed", 0, "Не удалось обновить", err.Error())
			return
		}
		u.set("downloading", 0, "Скачивание обновления…", "")
		path, err := update.Download(ctx, manifest.APKURL, manifest.SHA256, "MS7-update-*.apk", func(done, total int64) {
			if total > 0 {
				percent := int(done * 100 / total)
				u.set("downloading", percent, fmt.Sprintf("Скачивание: %d%%", percent), "")
			}
		})
		if err != nil {
			u.set("failed", 0, "Не удалось обновить", err.Error())
			return
		}
		if err := ms7app.RunInstaller(path); err != nil {
			u.set("failed", 0, "Не удалось обновить", err.Error())
			return
		}
		u.set("starting", 100, "Открыт установщик Android", "")
	}()
	ok(w, map[string]bool{"started": true})
}
