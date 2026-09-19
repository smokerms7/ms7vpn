package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"ms7vpn/internal/model"
	"ms7vpn/internal/update"
)

// Самообновление: приложение скачивает установщик и передаёт работу ему.
//
// Почему не обновляем файлы сами: программа лежит в той же папке, что и
// MS7VPN.exe, а Windows не даёт переписать работающий файл. Установщик —
// отдельный процесс, он сначала закрывает программу, потом заменяет файлы и
// запускает её снова. Заодно он же обновит ярлыки и запись в списке
// установленных программ.
//
// Установщик небольшой: сами файлы программы он скачивает уже сам и
// показывает при этом своё окно с полосой. Здесь скачиваются только эти
// пара мегабайт.

// updateStage — на каком шаге находится обновление.
const (
	updateStageIdle        = "idle"
	updateStageChecking    = "checking"
	updateStageDownloading = "downloading"
	updateStageStarting    = "starting"
	updateStageFailed      = "failed"
)

// UpdateProgress — то, что видит интерфейс, пока идёт обновление.
type UpdateProgress struct {
	Stage   string `json:"stage"`
	Percent int    `json:"percent"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version,omitempty"`
}

// Состояние одно на процесс: вторая копия программы не запускается
// (см. internal/singleton), поэтому второго обновления быть не может.
var selfUpdate struct {
	mu       sync.Mutex
	running  bool
	progress UpdateProgress
}

func updateState() UpdateProgress {
	selfUpdate.mu.Lock()
	defer selfUpdate.mu.Unlock()
	if selfUpdate.progress.Stage == "" {
		return UpdateProgress{Stage: updateStageIdle}
	}
	return selfUpdate.progress
}

func setUpdateState(progress UpdateProgress) {
	selfUpdate.mu.Lock()
	selfUpdate.progress = progress
	selfUpdate.mu.Unlock()
}

func failUpdate(version, message string) {
	setUpdateState(UpdateProgress{
		Stage:   updateStageFailed,
		Version: version,
		Message: "Не удалось обновить",
		Error:   message,
	})
	selfUpdate.mu.Lock()
	selfUpdate.running = false
	selfUpdate.mu.Unlock()
}

// handleUpdateProgress отдаёт ход обновления. Интерфейс опрашивает его,
// пока идёт скачивание.
func (a *App) handleUpdateProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}
	writeOK(w, updateState())
}

// handleInstallUpdate скачивает установщик новой версии и запускает его.
//
// Отвечает сразу, не дожидаясь конца скачивания: иначе на медленном канале
// запрос отвалился бы по таймауту. Ход работы смотрят через
// /api/update/progress.
func (a *App) handleInstallUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, methodError())
		return
	}

	selfUpdate.mu.Lock()
	if selfUpdate.running {
		current := selfUpdate.progress
		selfUpdate.mu.Unlock()
		writeOK(w, current)
		return
	}
	selfUpdate.running = true
	selfUpdate.progress = UpdateProgress{
		Stage:   updateStageChecking,
		Message: "Проверка последней версии",
	}
	selfUpdate.mu.Unlock()

	source := a.Snapshot().Settings.UpdateURL
	go a.runSelfUpdate(source)

	writeOK(w, updateState())
}

func (a *App) runSelfUpdate(source string) {
	// Времени даём много: канал может быть медленным, а установщик всё же
	// пара мегабайт. Само скачивание прерывается только этим сроком.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	result, err := update.Check(ctx, source, model.AppVersion)
	if err != nil {
		a.logf("обновление: проверка не удалась: %v", err)
		failUpdate("", err.Error())
		return
	}
	if !result.HasUpdate {
		a.logf("обновление: уже установлена последняя версия %s", result.Current)
		failUpdate(result.Current, "Установлена последняя версия "+result.Current)
		return
	}
	if result.DownloadURL == "" {
		a.logf("обновление: в выпуске %s нет установщика", result.Latest)
		failUpdate(result.Latest, "В выпуске "+result.Latest+" нет файла установщика")
		return
	}

	setUpdateState(UpdateProgress{
		Stage:   updateStageDownloading,
		Version: result.Latest,
		Message: "Скачивание установщика",
	})
	a.logf("обновление: скачиваю %s", result.DownloadURL)

	path, err := update.Download(ctx, result.DownloadURL, result.SHA256,
		"MS7VPN-Setup-*.exe", func(done, total int64) {
			progress := UpdateProgress{
				Stage:   updateStageDownloading,
				Version: result.Latest,
				Message: "Скачивание установщика",
			}
			if total > 0 {
				progress.Percent = int(100 * done / total)
				progress.Message = fmt.Sprintf("Скачивание установщика: %d%%", progress.Percent)
			}
			setUpdateState(progress)
		})
	if err != nil {
		a.logf("обновление: скачать не удалось: %v", err)
		failUpdate(result.Latest, err.Error())
		return
	}

	setUpdateState(UpdateProgress{
		Stage:   updateStageStarting,
		Version: result.Latest,
		Percent: 100,
		Message: "Запуск установщика",
	})
	a.logf("обновление: запускаю установщик %s", path)

	if err := RunInstaller(path); err != nil {
		a.logf("обновление: установщик не запустился: %v", err)
		_ = os.Remove(path)
		failUpdate(result.Latest, err.Error())
		return
	}

	// Установщик сам закроет программу, чтобы заменить файлы, но лучше уйти
	// самим: так подключение разрывается по-человечески и системный прокси
	// возвращается на место.
	time.Sleep(1200 * time.Millisecond)
	a.Shutdown()
}
