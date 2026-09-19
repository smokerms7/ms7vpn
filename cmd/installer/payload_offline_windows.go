//go:build windows && offline

// Полный установщик: файлы программы лежат внутри него.
//
// Собирается с тегом offline (см. BUILD_WINDOWS.bat). Нужен там, где нет
// интернета или не открывается GitHub.
package main

import (
	"embed"
	"fmt"

	"ms7vpn/internal/installui"
)

// Встраиваемые файлы лежат в отдельной папке и собираются скриптом сборки.
// Папка хранится в репозитории с одним пустым файлом: так «go build» проходит
// сразу после клонирования, а сами артефакты (они весят десятки мегабайт)
// в репозиторий не попадают.
//
//go:embed all:payload
var payloadFS embed.FS

func installHint(upgrade bool) string {
	if upgrade {
		return "Полная версия установщика: интернет не нужен. " +
			"Подписки и настройки сохранятся."
	}
	return "Полная версия установщика: все файлы уже внутри, интернет не нужен."
}

func obtainPayload(report installui.Reporter) ([]byte, error) {
	report(40, "Подготовка файлов")
	data, err := payloadFS.ReadFile("payload/payload.zip")
	if err != nil {
		return nil, fmt.Errorf("установщик собран без payload.zip — запустите BUILD_WINDOWS.bat")
	}
	report(70, "Подготовка файлов")
	return data, nil
}
