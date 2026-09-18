//go:build windows

// Отдельный деинсталлятор MS7VPN.
//
// Его единственная задача — удалить программу. Он весит около двух мегабайт
// вместо двадцати пяти, которые занимала копия полного установщика.
package main

import (
	"os"

	"ms7vpn/internal/setup"
)

func main() {
	if err := setup.Uninstall(); err != nil {
		setup.Message("Удаление MS7VPN", "Не удалось удалить программу:\n"+err.Error(), true)
		os.Exit(1)
	}
	setup.Message("MS7VPN", "Программа удалена.", false)
}
