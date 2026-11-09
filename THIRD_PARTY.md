# Third-party components

## sing-box

MS7VPN bundles and/or downloads the unmodified official sing-box 1.14.4
Windows amd64 binary from [SagerNet/sing-box](https://github.com/SagerNet/sing-box/releases/tag/v1.14.4).
Copyright © 2022 nekohasekai. Distributed under GPL-3.0-or-later;
the complete license is included at `resources/sing-box/LICENSE`.
Corresponding source: [v1.14.4](https://github.com/SagerNet/sing-box/tree/v1.14.4).
Optional libcronet.dll from the upstream archive is not bundled; this client
does not enable Naive outbound.

MS7VPN downloads and executes Xray-core from the official XTLS/Xray-core release. Xray-core is distributed under Mozilla Public License 2.0.

TUN mode uses the official signed Wintun DLL. Wintun's downloadable prebuilt package contains its redistribution license. MS7VPN downloads that official package without modifying the driver.

## Режим «Запрет»

Режим не содержит своего кода обхода: он запускает готовый `winws` из проекта
[zapret](https://github.com/bol-van/zapret) (bol-van) дочерним процессом.
Бинарники взяты из официальной сборки
[zapret-win-bundle](https://github.com/bol-van/zapret-win-bundle), папка
`zapret-winws`, версия v72.12; `winws.exe` переименован в `shade-winws.exe`,
чтобы зачистка остатков не снимала обход, запущенный самим пользователем из
другой программы.

- `zapret` — MIT License, © bol-van.
- `WinDivert` (`WinDivert.dll`, `WinDivert64.sys`) — LGPL v3 / GPL v2,
  © Basil `basil00`. Драйвер и библиотека распространяются без изменений.
- `cygwin1.dll` — LGPL v3, проект Cygwin. Нужен потому, что `winws` собран
  под cygwin.
- Стратегии (`strategies.json`), списки доменов (`lists/`) и поддельные
  пакеты (`fake/`) — из сборки
  [zapret-discord-youtube](https://github.com/Flowseal/zapret-discord-youtube)
  (Flowseal), MIT License. Коммиты первоисточников записаны в
  `strategies.json`, поле `source`.

Тексты лицензий лежат рядом с файлами: `resources/zapret/licenses/`.

## simple-icons

Значки популярных программ в разделе «Без VPN» (internal/webui/assets/appicons.js) — пути из simple-icons 16.31.0, https://simpleicons.org, лицензия CC0-1.0. Товарные знаки принадлежат их владельцам.

