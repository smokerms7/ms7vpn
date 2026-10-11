Файлы режима «Запрет» (только Windows). Обновляются скриптом:

    python tools/zapret-import.py <zapret-discord-youtube> <zapret-win-bundle/zapret-winws>

- shade-winws.exe, cygwin1.dll, WinDivert.dll, WinDivert64.sys - из
  github.com/bol-van/zapret-win-bundle (zapret-winws), winws переименован,
  чтобы зачистка клиента не трогала чужой запущенный Запрет. Лицензии:
  zapret - MIT, WinDivert - LGPL v3.
- strategies.json, lists/, fake/ - стратегии и списки из
  github.com/Flowseal/zapret-discord-youtube (MIT), коммиты - в
  strategies.json, поле source.
