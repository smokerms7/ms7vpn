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
		log.Printf("MS7VPN web UI: %v", err)
		if err := winui.Run(application, target); err != nil {
			log.Printf("MS7VPN UI: %v", err)
		}
	}
}
