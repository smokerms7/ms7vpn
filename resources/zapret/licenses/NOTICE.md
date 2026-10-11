# Лицензии файлов режима «Запрет»

| Файлы | Проект | Лицензия | Текст |
|---|---|---|---|
| `shade-winws.exe` (это переименованный `winws.exe`) | [zapret](https://github.com/bol-van/zapret), bol-van | MIT | `flowseal-LICENSE.txt` |
| `strategies.json`, `lists/`, `fake/` | [zapret-discord-youtube](https://github.com/Flowseal/zapret-discord-youtube), Flowseal | MIT | `flowseal-LICENSE.txt` |
| `WinDivert.dll`, `WinDivert64.sys` | [WinDivert](https://github.com/basil00/WinDivert), basil00 | LGPL v3 **или** GPL v2 — на выбор | `WinDivert-LICENSE.txt` |
| `cygwin1.dll` | [Cygwin](https://cygwin.com/licensing.html) | LGPL v3 | `LGPL-3.0.txt` |

Отдельного файла лицензии в репозитории `bol-van/zapret` нет — MIT объявлена в
его README. Текст MIT с обоими правообладателями (bol-van и Flowseal) лежит в
`flowseal-LICENSE.txt`: он же покрывает и `winws`, и стратегии.

WinDivert и cygwin1.dll распространяются без изменений, отдельными файлами
рядом с программой, и заменяются пользователем на свои сборки — этого требует
LGPL. Исходники: ссылки выше.

Стратегии и списки со временем устаревают: провайдеры меняют оборудование, а
Flowseal выпускает новые наборы. Обновляются они переносом файлов из свежей
сборки Flowseal и бандла bol-van — скрипта для этого в репозитории пока нет.
Коммиты, из которых собран текущий набор, записаны в `strategies.json`, поле
`source`.
