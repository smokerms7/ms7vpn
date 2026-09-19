//go:build windows && !offline

// Онлайн-установщик: файлы программы скачиваются во время установки.
//
// Так установщик весит пару мегабайт вместо двадцати с лишним, и его удобно
// отдавать с сайта. Для установки нужен интернет; у кого его нет или у кого
// GitHub недоступен, есть полная сборка MS7VPN-Setup-Full.exe.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"ms7vpn/internal/installui"
	"ms7vpn/internal/update"
)

// payloadSource — откуда брать файлы программы.
//
// Сейчас это только GitHub. Если понадобится зеркало (например, когда у
// провайдера не открывается objects.githubusercontent.com), сюда добавляется
// второй адрес, и loadPayload перебирает их по очереди.
const payloadSource = "https://github.com/smokerms7/ms7vpn"

func installHint(upgrade bool) string {
	if upgrade {
		return "Новая версия (около 18 МБ) будет скачана с GitHub. " +
			"Подписки и настройки сохранятся."
	}
	return "Файлы программы (около 18 МБ) будут скачаны с GitHub. " +
		"Нужен доступ в интернет. Если скачать не получится, возьмите " +
		"полную версию MS7VPN-Setup-Full.exe на vpn.ms7pc.shop."
}

func obtainPayload(report installui.Reporter) ([]byte, error) {
	report(5, "Поиск последней версии")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	payload, err := update.LatestPayload(ctx, payloadSource, "installer")
	if err != nil {
		return nil, downloadProblem(err)
	}

	report(8, "Скачивание файлов программы")
	path, err := update.Download(ctx, payload.URL, payload.SHA256, "ms7vpn-payload-*.zip",
		func(done, total int64) {
			if total <= 0 {
				report(8, fmt.Sprintf("Скачивание: %s", megabytes(done)))
				return
			}
			// Скачивание занимает отрезок от 8 до 68 процентов.
			report(8+int(60*done/total),
				fmt.Sprintf("Скачивание: %s из %s", megabytes(done), megabytes(total)))
		})
	if err != nil {
		return nil, downloadProblem(err)
	}
	defer os.Remove(path)

	report(70, "Проверка архива")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// downloadProblem превращает сетевую ошибку в объяснение, из которого понятно,
// что делать дальше. Без этого человек видел бы «dial tcp: i/o timeout».
func downloadProblem(err error) error {
	return fmt.Errorf("Не удалось скачать файлы программы.\n\n%s\n\n"+
		"Проверьте подключение к интернету. Если GitHub у вашего провайдера "+
		"не открывается, скачайте полную версию MS7VPN-Setup-Full.exe "+
		"на vpn.ms7pc.shop — ей интернет не нужен.", shortError(err))
}

func shortError(err error) string {
	text := err.Error()
	// Сетевые ошибки Go длинные и с адресами: оставляем последнюю часть,
	// она обычно и есть суть («connection refused», «no such host»).
	if index := strings.LastIndex(text, ": "); index > 0 && len(text)-index < 60 {
		text = text[index+2:]
	}
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

func megabytes(value int64) string {
	return fmt.Sprintf("%.1f МБ", float64(value)/(1<<20))
}
