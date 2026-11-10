/* MS7VPN для Android — подключается перед app.js. Тот же интерфейс, что на ПК;
   здесь только то, чем телефон отличается: вместо exe — пакеты приложений. */
(() => {
  'use strict';
  window.MS7_PLATFORM = 'android';

  // Популярные приложения для «Без VPN». Значки Steam, Discord, Telegram,
  // торрентов и браузеров — из общего набора; у остальных — буква на фирменном цвете.
  const desktop = {};
  for (const item of window.MS7AppIcons || []) desktop[item.name] = item;
  const pick = (name, pkg, hex) => {
    const base = desktop[name];
    return { name, exe: pkg, hex: (base && base.hex) || hex, d: base ? base.d : '' };
  };
  window.MS7AppIcons = [
    pick('Сбербанк', 'ru.sberbankmobile', '#21A038'),
    pick('Т-Банк', 'com.idamob.tinkoff.android', '#333333'),
    pick('ВТБ', 'ru.vtb24.mobilebanking.android', '#0A2896'),
    pick('Альфа-Банк', 'ru.alfabank.mobile.android', '#EF3124'),
    pick('Госуслуги', 'ru.rostel', '#0D4CD3'),
    pick('ВКонтакте', 'com.vkontakte.android', '#0077FF'),
    pick('Ozon', 'ru.ozon.app.android', '#005BFF'),
    pick('Wildberries', 'com.wildberries.ru', '#CB11AB'),
    pick('Авито', 'com.avito.android', '#00AAFF'),
    pick('Яндекс Go', 'ru.yandex.taxi', '#FFCC00'),
    pick('Кинопоиск', 'ru.kinopoisk', '#FF5500'),
    pick('Steam', 'com.valvesoftware.android.steam.community', '#2A2A35'),
    pick('Discord', 'com.discord', '#5865F2'),
    pick('Telegram', 'org.telegram.messenger', '#26A5E4'),
    pick('Chrome', 'com.android.chrome', '#4285F4')
  ];
  window.MS7AppLabels = {};
})();
