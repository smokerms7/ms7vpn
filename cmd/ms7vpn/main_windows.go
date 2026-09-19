//go:build windows

package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	ms7app "ms7vpn/internal/app"
	"ms7vpn/internal/singleton"
	"ms7vpn/internal/webui"
	"ms7vpn/internal/winui"
)

// openLogFile открывает журнал приложения на дозапись.
// Ошибку глотаем: без журнала программа обязана работать.
func openLogFile(dataDir string) *os.File {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil
	}
	file, err := os.OpenFile(filepath.Join(dataDir, "ms7vpn.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return file
}

func main() {
	autoConnect := flag.String("autoconnect", "", "node ID to connect after an elevated restart")
	elevated := flag.Bool("elevated", false, "marks an elevated relaunch")
	flag.Parse()

	// Вторая копия не запускается: она поднимала ещё одно окно поверх той же
	// папки профиля WebView2, из-за чего окно оставалось белым, и обе копии
	// дрались за сетевой адаптер. Вместо запуска показываем уже открытое окно.
	//
	// При перезапуске с правами администратора старая копия ещё закрывается,
	// поэтому ждём освобождения несколько секунд.
	wait := time.Duration(0)
	if *elevated {
		wait = 6 * time.Second
	}
	if !singleton.Acquire(wait) {
		singleton.SignalExisting()
		return
	}
	defer singleton.Release()

	base, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dataDir := filepath.Join(base, "MS7VPN")

	// Программа собрана с -H windowsgui: консоли нет, и всё, что пишет
	// стандартный log, уходило в никуда. Из-за этого белое окно WebView2
	// было нечем объяснить — в журнале не оставалось ни строчки, хотя
	// библиотека пишет туда и ошибки, и log.Fatal. Направляем этот вывод
	// в тот же файл, что ведёт само приложение.
	if file := openLogFile(dataDir); file != nil {
		defer file.Close()
		log.SetOutput(file)
		log.SetFlags(0)
		log.SetPrefix("")
	}

	application, err := ms7app.New(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer application.Shutdown()

	application.StartBackgroundTasks()

	// «Подключаться при запуске» из настроек. Раньше эта галочка в ветке с
	// окном WebView2 не читалась вообще.
	target := *autoConnect
	if target == "" {
		target = application.AutoConnectNodeID()
	}

	// Основное окно — WebView2 с фирменным интерфейсом.
	// Если WebView2 в системе нет, откатываемся на старое нативное окно.
	if err := webui.Run(application, target); err != nil {
		log.Printf("%s  окно WebView2 не открылось: %v",
			time.Now().Format("2006-01-02 15:04:05"), err)
		if err := winui.Run(application, target); err != nil {
			log.Printf("%s  запасное окно тоже не открылось: %v",
				time.Now().Format("2006-01-02 15:04:05"), err)
		}
	}
}
