/* MS7VPN для Android — английский язык интерфейса.
   Интерфейс общий с ПК и написан по-русски, поэтому перевод — словарём:
   наблюдатель меняет текст на экране, в том числе появившийся позже
   (всплывашки, статусы). Строки с числами — по шаблонам. Язык хранится в
   профиле окна; смена языка перезагружает страницу. */
(() => {
  'use strict';
  let lang = 'ru';
  const fromUrl = new URLSearchParams(location.search).get('lang');
  try { lang = fromUrl || localStorage.getItem('ms7-lang') || 'ru'; } catch (_) { lang = fromUrl || 'ru'; }
  window.MS7Lang = lang;
  window.MS7SetLang = (next) => {
    try { localStorage.setItem('ms7-lang', next); } catch (_) { /* не запомним */ }
    try { if (window.MS7Native && window.MS7Native.setLang) window.MS7Native.setLang(next); } catch (_) { /* старая оболочка */ }
    const url = new URL(location.href);
    url.searchParams.set('lang', next);
    location.replace(url.toString());
  };
  if (lang !== 'en') return;
  document.documentElement.lang = 'en';

  const D = {
    "Через VPN идут только выбранные приложения, остальные — напрямую.": "Only selected apps go through the VPN, the rest go directly.",
    "Какие приложения пойдут через VPN в режиме «Вкл»": "Which apps go through the VPN in “On” mode",
    "Правила маршрутизации": "Routing rules",
    "Использовать маршрутизацию": "Use routing",
    "Выключите — весь трафик пойдёт через VPN без исключений": "Turn off to send all traffic through the VPN with no exceptions",
    "Отправлять HWID": "Send HWID",
    "Идентификатор устройства для лимита устройств в подписке": "Device ID for the subscription device limit",
    "Mux": "Mux",
    "Несколько соединений в одном — быстрее открываются сайты. Не работает с XTLS Vision и XHTTP": "Several connections in one — sites open faster. Not used with XTLS Vision and XHTTP",
    "Фрагментация": "Fragmentation",
    "Дробит начало TLS-соединения — помогает, если провайдер режет VPN": "Splits the start of TLS connections — helps if the ISP throttles VPN",
    "Запускать при включении телефона": "Start when the phone boots",
    "Сразу подключаться к VPN после перезагрузки": "Connect to the VPN right after reboot",
    "Автоподключение к серверу": "Auto-connect server",
    "Последний": "Last used",
    "Самый быстрый": "Fastest",
    "Стратегия доменов": "Domain strategy",
    "IPIfNonMatch — по умолчанию": "IPIfNonMatch — default",
    "AsIs — только домены": "AsIs — domains only",
    "IPOnDemand — сразу по IP": "IPOnDemand — resolve to IP",
    "Отображение пинга": "Ping display",
    "Время": "Time",
    "Значки": "Icons",
    "Работа в фоне": "Background work",
    "Отключить оптимизацию батареи, чтобы Android не обрывал VPN": "Disable battery optimization so Android does not kill the VPN",
    "Оптимизация батареи уже отключена — VPN не будет обрываться": "Battery optimization is already off — the VPN will not be killed",
    "Постоянный VPN": "Always-on VPN",
    "Системные настройки Android: «Постоянная VPN» и «Блокировать без VPN»": "Android system settings: “Always-on VPN” and “Block connections without VPN”",
    "VPN будет включаться вместе с телефоном": "The VPN will start with the phone",
    "Автозапуск выключен": "Autostart is off",
    "Для режима «Вкл» в настройках": "For “On” mode in settings",
    "Приложения через VPN": "Apps via VPN",
    "Когда в настройках включено «Прокси для выбранных приложений», через VPN идут только эти приложения, остальные — напрямую. Изменения применятся при следующем подключении.": "When “Per-app proxy” is on in settings, only these apps go through the VPN, the rest go directly. Changes apply on the next connection.",
    "Включите «Российские сайты напрямую» в правилах маршрутизации. Или включите в настройках «Прокси для выбранных приложений» и оставьте в списке только нужные — остальные пойдут напрямую.": "Turn on “Russian sites directly” in the routing rules. Or enable “Per-app proxy” in settings and keep only the apps you need — the rest go directly.",
    "Настройки туннеля": "Tunnel settings",
    "Обновлять подписки при запуске": "Update subscriptions on launch",
    "Сниффинг": "Sniffing",
    "Определять сайт в трафике — нужно для правил по доменам": "Detect sites in traffic — needed for domain rules",
    "Разрешить подключения из LAN": "Allow LAN connections",
    "Другие устройства в сети смогут ходить через телефон: SOCKS5 :10808, HTTP :10809": "Other devices on the network can use the phone: SOCKS5 :10808, HTTP :10809",
    "Прокси для выбранных приложений": "Per-app proxy",
    "Выкл": "Off",
    "Вкл": "On",
    "Обход": "Bypass",
    "Через VPN идут все приложения.": "All apps go through the VPN.",
    "Через VPN идут только выбранные приложения, остальные — напрямую.": "Only selected apps go through the VPN, the rest go directly.",
    "Выбранные приложения идут напрямую, остальные — через VPN.": "Selected apps go directly, the rest through the VPN.",
    "Выбрать приложения": "Choose apps",
    "Список для режима «Вкл» или «Обход»": "List for “On” or “Bypass” mode",
    "MTU туннеля": "Tunnel MTU",
    "От 1280 до 9000. Если сайты грузятся не до конца — уменьшите до 1400.": "From 1280 to 9000. If sites load partially, lower it to 1400.",
    "Маршрутизация": "Routing",
    "Российские сайты напрямую, свои списки сайтов": "Russian sites directly, your own site lists",
    "Telegram-бот": "Telegram bot",
    "Подписка, оплата и поддержка": "Subscription, payment and support",
    "Telegram-канал": "Telegram channel",
    "Новости MS7VPN": "MS7VPN news",
    "Порядок, удаление, бесплатная подписка": "Order, deletion, free subscription",
    "MTU — число от 1280 до 9000": "MTU must be a number from 1280 to 9000",
    'Главная': 'Home', 'Серверы': 'Servers', 'Подписки': 'Subscriptions', 'Без VPN': 'Bypass VPN', 'вкл': 'on',
    'Статистика': 'Statistics', 'Информация': 'Info', 'Журнал': 'Log', 'Настройки': 'Settings',
    'Свобода без границ': 'Freedom without borders', 'Добро пожаловать': 'Welcome', 'MS7VPN готов к работе': 'MS7VPN is ready',
    'Отключено': 'Disconnected', 'Добавить подписку из ссылки?': 'Add subscription from link?', 'Отмена': 'Cancel',
    'Добавить': 'Add', 'Доступна новая версия': 'A new version is available', 'Нажмите, чтобы обновиться': 'Tap to update',
    'Подключить': 'Connect', 'Нажмите, чтобы подключиться': 'Tap to connect', 'Сервер не выбран': 'No server selected',
    'сменить ›': 'change ›', 'Пинг': 'Ping', '↓ Загрузка': '↓ Download', '↑ Отдача': '↑ Upload', 'до': 'until',
    'Активна': 'Active', 'Обновить': 'Refresh', '⚡ Самый быстрый': '⚡ Fastest', 'Проверить пинг': 'Test ping',
    'Серверы появятся после добавления подписки.': 'Servers will appear after you add a subscription.',
    'Добавить подписку': 'Add subscription', 'Обновить все': 'Refresh all', 'Ссылка подписки из бота': 'Subscription link from the bot',
    '🎁 Бесплатная подписка': '🎁 Free subscription', '👑 Купить в боте': '👑 Buy in the bot', 'Подписок пока нет.': 'No subscriptions yet.',
    'Что идёт мимо туннеля': 'What bypasses the tunnel', 'Российские сайты напрямую': 'Russian sites directly',
    'Зоны .ru, .su, .рф, Яндекс, VK, банки, Госуслуги, маркетплейсы — без VPN: родной пинг и никаких «вы из-за границы»': '.ru, .su, .рф zones, Yandex, VK, banks, Gosuslugi, marketplaces — without VPN: native ping and no “you are abroad”',
    'Напрямую': 'Direct', 'Всегда через VPN': 'Always via VPN',
    'Сайты, которые всегда идут без VPN. По одному в строке, можно вставлять ссылки целиком — домен вырежется сам. Поддомены входят.': 'Sites that always bypass the VPN. One per line; you can paste full links — the domain is extracted automatically. Subdomains included.',
    'Важнее всех остальных правил: так можно оставить в туннеле отдельный сайт из российских.': 'Overrides all other rules: keep a specific Russian site inside the tunnel.',
    'Что можно писать в списках: домен или ссылку': 'Allowed in the lists: a domain or link', ', IP-адрес или подсеть': ', an IP address or subnet',
    ', часть имени': ', part of a name', ', категорию': ', a category', 'или страну': 'or a country',
    '(категории и страны — только в полной установке). Строки с': '(categories and countries need geo files). Lines starting with', '— комментарии.': 'are comments.',
    'Приложения мимо VPN': 'Apps bypassing VPN', 'Работает для всего телефона': 'Works for the whole phone',
    'Из установленных': 'From installed', 'Скрыть список': 'Hide list', 'Популярные — нажмите, чтобы добавить': 'Popular — tap to add',
    'Трафик этих приложений пойдёт напрямую, как будто VPN выключен — удобно для банков, игр и торрентов. Изменения применятся при следующем подключении (если VPN включён — он переподключится сам).': 'Traffic of these apps goes directly as if the VPN were off — handy for banks, games and torrents. Changes apply on the next connection (if the VPN is on, it reconnects automatically).',
    'Изменения применятся сразу — при включённом VPN он переподключится сам.': 'Changes apply immediately — if the VPN is on, it reconnects automatically.',
    'Пакет, например org.telegram.messenger': 'Package, e.g. org.telegram.messenger',
    'Сохранить': 'Save', 'Очистить': 'Clear', 'Сегодня': 'Today', 'За 7 дней': 'Last 7 days', 'За 30 дней': 'Last 30 days',
    'Трафик по дням': 'Traffic by day', 'загрузка': 'download', 'отдача': 'upload', 'Всего подключений': 'Total connections',
    'Время в VPN за всё время': 'Total time in VPN', 'Средний пинг': 'Average ping', 'Трафик за всё время': 'Total traffic',
    'Оформление': 'Theme', 'Основная': 'Brand', 'Светлая': 'Light', 'Тёмная': 'Dark', 'Цвет применяется сразу.': 'Applied instantly.',
    'Обновлять подписки автоматически': 'Update subscriptions automatically', 'Подключаться при запуске': 'Connect on launch',
    'Блокировать рекламу и трекеры': 'Block ads and trackers', 'Проверять пинг серверов при запуске': 'Test server ping on launch',
    'Размер текста': 'Text size', 'Мелкий': 'Small', 'Обычный': 'Normal', 'Крупный': 'Large', 'Порядок серверов': 'Server order',
    'По пингу — быстрые наверху': 'By ping — fastest first', 'По названию, А–Я': 'By name, A–Z', 'Как в подписке': 'As in subscription',
    'Яндекс': 'Yandex', 'Проверка пинга': 'Ping test', 'Через сервер · GET — точнее всего': 'Through server · GET — most accurate',
    'Через сервер · HEAD — легче': 'Through server · HEAD — lighter', 'TCP — только порт сервера': 'TCP — server port only',
    'Ждать ответа': 'Timeout', '3 секунды': '3 seconds', '5 секунд': '5 seconds', '8 секунд': '8 seconds', '12 секунд': '12 seconds',
    '«Через сервер» — через каждый сервер открывается адрес проверки: так видно настоящую скорость отклика. TCP проверяет только, открыт ли порт.': '“Through server” opens the test URL through each server, showing the real response time. TCP only checks that the port is open.',
    'Адрес проверки соединения и пинга': 'Connection and ping test URL', 'Сбросить настройки': 'Reset settings',
    'О приложении и устройстве': 'About the app and device', 'MS7VPN для Android': 'MS7VPN for Android', 'Ядро проверяется…': 'Checking core…',
    'Версия приложения': 'App version', 'Выбранное ядро': 'Core', 'Устройство': 'Device', 'Система': 'System',
    'Идентификатор устройства': 'Device ID', 'копировать': 'copy', 'Проверка обновлений': 'Updates', 'Нажмите «Проверить»': 'Tap “Check”',
    'Проверить': 'Check', 'Скачать': 'Download', 'Telegram-бот и канал': 'Telegram bot and channel',
    'Подписка, оплата, новости и поддержка': 'Subscription, payment, news and support', 'Открыть бота': 'Open bot', 'Открыть канал': 'Open channel',
    'Частые вопросы': 'FAQ', 'Как быстро подключиться к лучшему серверу?': 'How to quickly connect to the best server?',
    'Удерживайте большую кнопку секунду или нажмите «Самый быстрый» на вкладке «Серверы» — приложение проверит пинг всех серверов и подключится к самому быстрому.': 'Hold the big button for a second — the app tests all servers and connects to the fastest one.',
    'Почему банк или Госуслуги пишут, что я за границей?': 'Why do banks say I am abroad?',
    'Включите «Российские сайты напрямую» на главной или в разделе «Без VPN». Российские сайты пойдут мимо туннеля, а всё остальное — через VPN.': 'Turn on “Russian sites directly” in “Bypass VPN”. Russian sites will bypass the tunnel, everything else goes via VPN.',
    'Как добавить подписку быстрее всего?': 'What is the fastest way to add a subscription?',
    'Скопируйте ссылку из бота и нажмите «Вставить» в разделе «Подписки». Или нажмите в боте кнопку «Открыть в приложении» — ссылка вида ms7vpn://add?url=… откроет MS7VPN и предложит добавить подписку.': 'Copy the link from the bot, tap “+” and choose “Paste from clipboard”. Or tap “Open in app” in the bot — an ms7vpn://add?url=… link opens MS7VPN and offers to add the subscription.',
    'Что значит «достигнут лимит устройств»?': 'What does “device limit reached” mean?',
    'Подписка уже подключена на максимальном числе устройств. Сбросьте устройства в Telegram-боте и нажмите «Обновить» у подписки.': 'The subscription is already used on the maximum number of devices. Reset devices in the Telegram bot and refresh the subscription.',
    'Android спрашивает разрешение на VPN': 'Android asks for VPN permission',
    'Это нормально: при первом подключении Android просит разрешить приложению создать VPN. Нажмите «ОК» — больше вопрос не появится. Значок ключа в строке состояния означает, что VPN включён.': 'This is normal: on the first connection Android asks to allow the app to create a VPN. Tap “OK” — it will not ask again. The key icon in the status bar means the VPN is on.',
    'Банк или игра работают плохо через VPN': 'A bank or game works poorly via VPN',
    'Добавьте приложение в «Без VPN → Приложения мимо VPN» — его трафик пойдёт напрямую.': 'Add the app to “Bypass VPN → Apps bypassing VPN” — its traffic will go directly.',
    'Последние события': 'Recent events', 'Журнал пуст.': 'The log is empty.', 'Остановить': 'Stop', 'Готово': 'Done', 'Сервер': 'Server',
    'Своё название (пусто — как в подписке)': 'Custom name (empty — as in subscription)', 'Копировать ссылку': 'Copy link',
    'Подключиться': 'Connect', 'Скрыть': 'Hide', 'Закрыть': 'Close', 'Поиск по названию': 'Search by name',
    'Удерживайте секунду — подключим самый быстрый сервер': 'Hold for a second — connect to the fastest server',
    'Дней до окончания подписки': 'Days until the subscription ends', 'без изменений': 'no changes', 'Обновлено:': 'Updated:',
    'Не удалось выполнить запрос': 'Request failed', 'Пока ничего не происходило.': 'Nothing has happened yet.', 'нет связи': 'no response',
    'мс': 'ms', 'ГБ': 'GB', 'МБ': 'MB', 'КБ': 'KB', 'Подключено': 'Connected', 'Подключение…': 'Connecting…', 'Отключить': 'Disconnect',
    'Отменить подключение': 'Cancel connection', 'Поиск…': 'Searching…', 'Подключение': 'Connecting', 'Выберите сервер': 'Choose a server',
    'Добавьте подписку': 'Add a subscription', 'Подключаемся…': 'Connecting…', 'Добавьте подписку в разделе «Подписки»': 'Tap “+” to add a subscription',
    'Добавьте подписку — ссылка из бота': 'Add a subscription — link from the bot', 'Не поддерживается': 'Not supported',
    'Гбит/с': 'Gbit/s', 'Мбит/с': 'Mbit/s', 'Кбит/с': 'Kbit/s', 'Мб/с': 'MB/s', 'Кб/с': 'KB/s', 'Собираем данные…': 'Collecting data…',
    'График появится после подключения': 'The chart appears after connecting', 'Доброй ночи': 'Good night', 'Доброе утро': 'Good morning',
    'Добрый день': 'Good afternoon', 'Добрый вечер': 'Good evening', 'сервер': 'server',
    'Нажмите кнопку, чтобы подключиться': 'Tap the button to connect', 'Истекла': 'Expired', 'Лимит исчерпан': 'Limit reached',
    'Отключена': 'Disabled', 'Истекает сегодня': 'Expires today', 'Подписка': 'Subscription', 'из': 'of', '∞ Безлимит': '∞ Unlimited',
    'без ограничений': 'unlimited', 'Лимит устройств': 'Device limit', 'использовано': 'used', 'Установлена версия': 'Installed version',
    'Выбранное ядро установлено': 'Core is installed', 'Ядро загрузится при первом подключении': 'Core loads on first connection',
    'Вставьте ссылку подписки': 'Paste a subscription link', 'Подписка добавлена': 'Subscription added', 'Обновляем…': 'Refreshing…',
    'Отключаемся…': 'Disconnecting…', 'Отключение…': 'Disconnecting…', 'Подключение отменено': 'Connection cancelled',
    'Сначала добавьте подписку': 'Add a subscription first', 'Ищем самый быстрый сервер…': 'Looking for the fastest server…',
    'Ни один сервер не ответил': 'No server responded', 'Пинг проверен': 'Ping tested', 'Подписка не выбрана': 'No subscription selected',
    'Идентификатор скопирован': 'ID copied', 'Не удалось скопировать': 'Copy failed', 'Подписок пока нет': 'No subscriptions yet',
    'Подписка удалена': 'Subscription deleted', 'Настройки сохранены': 'Settings saved', 'Проверяем…': 'Checking…',
    'Установлена последняя версия': 'You have the latest version', 'Обновлений нет': 'No updates', 'Не удалось проверить обновления': 'Could not check for updates',
    'Не удалось обновить': 'Update failed', 'Запуск установщика': 'Starting installer', 'Подтвердите установку в окне Android.': 'Confirm the installation in the Android dialog.',
    'Обновление': 'Update', 'Журнал очищен': 'Log cleared', 'Весь трафик идёт через выбранный сервер. Открывает всё, в том числе заблокированное по IP.': 'All traffic goes through the selected server. Opens everything, including sites blocked by IP.',
    'Россия': 'Russia', 'Сохранено — переподключаемся с новыми правилами': 'Saved — reconnecting with the new rules', 'Правила сохранены': 'Rules saved',
    'подключение': 'connection', 'подключения': 'connections', 'подключений': 'connections', 'в VPN': 'in VPN',
    'вс': 'Sun', 'пн': 'Mon', 'вт': 'Tue', 'ср': 'Wed', 'чт': 'Thu', 'пт': 'Fri', 'сб': 'Sat', 'сегодня': 'today',
    'Статистика очищена': 'Statistics cleared', 'Точно сбросить?': 'Reset for sure?', 'Настройки сброшены. Подписки остались на месте': 'Settings reset. Subscriptions were kept',
    'В буфере не ссылка подписки': 'The clipboard has no subscription link', 'Название в подписке': 'Name in subscription', 'Протокол': 'Protocol',
    'Адрес': 'Address', 'Транспорт': 'Transport', 'нет': 'none', 'Поддержка': 'Supported', 'Да': 'Yes', 'Нет': 'No',
    'Название сохранено': 'Name saved', 'Вернули название из подписки': 'Restored the subscription name', 'Ссылка сервера скопирована': 'Server link copied',
    'Загружаем список…': 'Loading list…', 'Пока ни одной — выберите из популярных или запущенных.': 'None yet — pick from popular or installed.',
    'Вставить': 'Paste', 'Назад': 'Back', 'Подписка обновлена': 'Subscription updated', 'Переключаемся…': 'Switching…', 'истекла': 'expired',
    'Серверов нет — обновите подписку': 'No servers — refresh the subscription', 'Ссылка из Telegram-бота — кнопка «+» вверху': 'Link from the Telegram bot — “+” button at the top',
    'Туннель': 'Tunnel', 'Интерфейс': 'Interface', 'Расширенные настройки': 'Advanced settings', 'Другие настройки': 'Other settings',
    'Российские сайты, свои сайты и приложения мимо туннеля': 'Russian sites, your sites and apps bypassing the tunnel',
    'Добавить, удалить, порядок': 'Add, delete, order', 'Трафик и время в VPN': 'Traffic and time in VPN', 'События и ошибки': 'Events and errors',
    'О приложении': 'About', 'Версия, устройство, обновления, помощь': 'Version, device, updates, help',
    'Оформление, язык, размер текста, порядок серверов': 'Theme, language, text size, server order', 'Без VPN, блокировка рекламы, DNS': 'Bypass VPN, ad blocking, DNS',
    'Проверка пинга, адрес проверки': 'Ping test, test URL', 'Автообновление, автоподключение, подписки, журнал': 'Auto-update, auto-connect, subscriptions, log',
    'Язык': 'Language', 'Тест пинга': 'Ping test', 'Обновить подписку': 'Refresh subscription', 'Добавить подписку или сервер': 'Add subscription or server',
    'URL подписки': 'Subscription URL', 'Ссылка https://… из Telegram-бота': 'An https://… link from the Telegram bot',
    'Вставить из буфера обмена': 'Paste from clipboard', 'Ссылка подписки или сервера из буфера': 'Subscription or server link from the clipboard',
    'QR-код': 'QR code', 'Отсканируйте QR-код камерой': 'Scan a QR code with the camera', 'Ручной ввод': 'Manual input',
    'Ссылка сервера vless://, vmess://, trojan://, ss://, hy2://': 'Server link vless://, vmess://, trojan://, ss://, hy2://',
    'Название (необязательно)': 'Name (optional)', 'Ссылка сервера': 'Server link', 'Буфер обмена пуст': 'The clipboard is empty',
    'Сканер QR-кода недоступен': 'QR scanner is unavailable', 'Добавляем…': 'Adding…', 'Ссылка подписки': 'Subscription link',
    'Сервер добавлен': 'Server added', 'Вставьте ссылку сервера': 'Paste a server link', 'Удалить': 'Delete', 'Активная подписка': 'Active subscription',
    'Соединение защищено': 'Connection secured', 'Сначала выберите сервер': 'Choose a server first', 'Локальный профиль': 'Local profile',
    'Подписку уже добавили': 'Subscription already added', 'Удалить': 'Delete', 'безлимит': 'unlimited'
  };
  const P = [
    [/^(Доброй ночи|Доброе утро|Добрый день|Добрый вечер)(.*)$/, (m, g, rest) => D[g] + rest],
    [/^(\d+) сервер(?:а|ов)?$/, (m, n) => `${n} server${n === '1' ? '' : 's'}`],
    [/^(\d+) сервер(?:а|ов)? · обновлено (.+)$/, (m, n, d) => `${n} server${n === '1' ? '' : 's'} · updated ${d}`],
    [/^(.+) · (\d+) сервер(?:а|ов)?$/, (m, a, n) => `${tr1(a)} · ${n} server${n === '1' ? '' : 's'}`],
    [/^Осталось (\d+) (?:день|дня|дней)$/, (m, n) => `${n} day${n === '1' ? '' : 's'} left`],
    [/^ещё (\d+) дн\.$/, (m, n) => `${n} days left`],
    [/^(\d+) ч (\d+) мин$/, '$1 h $2 min'], [/^(\d+) мин$/, '$1 min'],
    [/^(.+) из (.+)$/, (m, a, b) => `${tr1(a)} of ${tr1(b)}`],
    [/^(.+) · безлимит$/, (m, a) => `${tr1(a)} · unlimited`],
    [/^(.+) · (.+) · (.+)$/, (m, a, b, c) => `${tr1(a)} · ${tr1(b)} · ${tr1(c)}`],
    [/^([\d,.]+) (ГБ|МБ|КБ)$/, (m, n, u) => `${n} ${D[u]}`],
    [/^Соединение защищено · (.+)$/, 'Connection secured · $1'],
    [/^Доступна версия (.+)$/, 'Version $1 is available'], [/^Установлена (.+)$/, 'Installed $1'], [/^Версия (.+)$/, 'Version $1'],
    [/^Активная подписка: (.+)$/, 'Active subscription: $1'], [/^Самый быстрый: (.+)$/, 'Fastest: $1'],
    [/^Обновлено: (.+)$/, 'Updated: $1'], [/^Загрузка ядра… (\d+)%$/, 'Loading core… $1%'],
    [/^\+(\d+) нов(?:ый|ых)$/, '+$1 new'], [/^−(\d+) убран[оа]?$/, '−$1 removed'],
    [/^· до (.+)$/, '· until $1'], [/^Скачивание: (\d+)%$/, 'Downloading: $1%'],
    [/^(\d+) (?:подключение|подключения|подключений)$/, '$1 connections'],
    [/^Переключаемся на (.+)…$/, 'Switching to $1…']
  ];
  const norm = (t) => t.replace(/\s+/g, ' ').trim();
  function tr1(t) {
    const key = norm(t);
    if (!key || !/[А-Яа-яЁё]/.test(key)) return t;
    if (D[key]) return D[key];
    for (const [re, rep] of P) {
      if (re.test(key)) return key.replace(re, rep);
    }
    return t;
  }
  window.MS7T = (t) => tr1(t);

  const SKIP = new Set(['SCRIPT', 'STYLE', 'TEXTAREA', 'PRE', 'CODE', 'INPUT']);
  function translateNode(node) {
    if (node.nodeType === 3) {
      const parent = node.parentNode;
      if (!parent || SKIP.has(parent.nodeName) || (parent.closest && parent.closest('.m-node-name, .chip-name, .srv-title, #srv-name, .log'))) return;
      const raw = node.nodeValue;
      const key = norm(raw);
      if (!key || !/[А-Яа-яЁё]/.test(key)) return;
      const out = tr1(key);
      if (out !== key) node.nodeValue = raw.replace(raw.trim(), out);
      return;
    }
    if (node.nodeType !== 1) return;
    for (const attr of ['placeholder', 'title', 'aria-label']) {
      const v = node.getAttribute(attr);
      if (v && /[А-Яа-яЁё]/.test(v)) {
        const out = tr1(v);
        if (out !== v) node.setAttribute(attr, out);
      }
    }
    if (SKIP.has(node.nodeName)) return;
    if (node.nodeName === 'OPTION') { const out = tr1(node.textContent); if (out !== node.textContent) node.textContent = out; return; }
    for (const child of node.childNodes) translateNode(child);
  }
  const start = () => {
    translateNode(document.body);
    new MutationObserver((records) => {
      for (const r of records) {
        if (r.type === 'characterData') translateNode(r.target);
        else if (r.type === 'attributes') translateNode(r.target);
        else for (const n of r.addedNodes) translateNode(n);
      }
    }).observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ['placeholder', 'title', 'aria-label'] });
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
