# MS7VPN для Windows

VPN-клиент на Go: ядро Xray, интерфейс на HTML в окне WebView2.

## Сборка

Нужен только Go 1.23 или новее. Зависимости лежат в `vendor/`, интернет для
сборки не требуется.

```
BUILD_WINDOWS.bat
```

Скрипт собирает приложение, деинсталлятор, упаковывает `payload.zip` и
установщик. Готовые файлы появятся в `dist\`.

Перед сборкой положите в папку `bundle\`:

| Файл | Откуда |
|---|---|
| `xray.exe` | [Xray-core v26.3.27](https://github.com/XTLS/Xray-core/releases/tag/v26.3.27), архив `Xray-windows-64.zip` |
| `wintun.dll` | [Wintun 0.14.1](https://www.wintun.net/), папка `bin/amd64` |
| `MicrosoftEdgeWebview2Setup.exe` | [Evergreen Bootstrapper](https://developer.microsoft.com/microsoft-edge/webview2/) — необязательно |

Эти файлы в репозиторий не кладутся: они большие и распространяются своими
проектами под своими лицензиями.

## Выпуск новой версии

Приложение проверяет обновления через GitHub: берёт последний выпуск этого
репозитория, номер версии — из метки выпуска, установщик — из вложений.

1. Поднять номер в `VERSION`, `internal/model/model.go` и
   `internal/setup/setup_windows.go`
2. Собрать: `BUILD_WINDOWS.bat`
3. Releases → Draft a new release
4. Tag: `ms7.vs1.3` (схема номеров — `ms7.vsМАЖОРНАЯ.МИНОРНАЯ`)
5. Приложить `dist\MS7VPN-Setup.exe`
6. Publish

Адрес проверки меняется в самой программе: Настройки → Проверка обновлений.
Кроме ссылки на репозиторий там принимается адрес своего файла `latest.json`
вида `{"version": "...", "url": "...", "sha256": "...", "notes": "..."}`.

## Устройство

| Папка | Что внутри |
|---|---|
| `cmd/ms7vpn` | точка входа приложения |
| `cmd/installer`, `cmd/uninstaller` | установщик и деинсталлятор |
| `internal/app` | подключение, состояние, локальный HTTP API |
| `internal/xray` | сборка конфигурации Xray, запуск ядра, счётчики трафика |
| `internal/subscription` | разбор подписок и запрос сведений у панели |
| `internal/webui` | окно WebView2, в `assets/` весь интерфейс |
| `internal/tray` | значок в области уведомлений |
| `internal/update` | проверка обновлений |
| `internal/singleton` | запрет второй копии |

Данные пользователя лежат в `%APPDATA%\MS7VPN`, программа — в
`%LOCALAPPDATA%\Programs\MS7VPN`.

## Проверки

```
go test ./...
```

Тесты не требуют сети. Отдельно можно прогнать разбор счётчиков трафика на
настоящем ядре:

```
set MS7_XRAY=C:\путь\к\xray.exe
go test ./internal/xray -run TestStatsAgainstRealCore -v
```

## Сторонние компоненты

Xray-core (MPL-2.0) и Wintun (GPL-2.0 с исключением) не входят в этот
репозиторий и распространяются своими правообладателями. Подробности —
в `THIRD_PARTY.md`.
