/* Флаги стран для списка серверов.
   Страна берётся из названия ноды в подписке: сначала эмодзи-флаг (🇳🇱),
   затем код страны (NL, DE), затем русское или английское название. */
(() => {
  'use strict';

  const rows = (colors) => colors.map((c, i) =>
    `<rect y="${(20 / colors.length) * i}" width="30" height="${20 / colors.length + 0.2}" fill="${c}"/>`).join('');
  const cols = (colors) => colors.map((c, i) =>
    `<rect x="${(30 / colors.length) * i}" width="${30 / colors.length + 0.2}" height="20" fill="${c}"/>`).join('');
  const cross = (bg, fg, inner) => `<rect width="30" height="20" fill="${bg}"/>` +
    `<rect x="8" width="6" height="20" fill="${fg}"/><rect y="7" width="30" height="6" fill="${fg}"/>` +
    (inner ? `<rect x="9.5" width="3" height="20" fill="${inner}"/><rect y="8.5" width="30" height="3" fill="${inner}"/>` : '');

  const FLAGS = {
    AE: `<rect width="30" height="20" fill="#fff"/><rect width="30" height="6.7" fill="#00732f"/><rect y="13.3" width="30" height="6.7" fill="#000"/><rect width="8" height="20" fill="#f00"/>`,
    AM: rows(['#d90012', '#0033a0', '#f2a800']),
    AT: rows(['#ed2939', '#fff', '#ed2939']),
    BE: cols(['#000', '#fdda24', '#ef3340']),
    BG: rows(['#fff', '#00966e', '#d62612']),
    CA: `<rect width="30" height="20" fill="#fff"/><rect width="7.5" height="20" fill="#d52b1e"/><rect x="22.5" width="7.5" height="20" fill="#d52b1e"/><path d="M15 5l1.2 2.8 2-.7-.7 3.2 2 1.3-2.2 1 .4 1.4h-2.1v2h-1v-2h-2.1l.4-1.4-2.2-1 2-1.3-.7-3.2 2 .7z" fill="#d52b1e"/>`,
    CH: `<rect width="30" height="20" fill="#da291c"/><rect x="13" y="4" width="4" height="12" fill="#fff"/><rect x="9" y="8" width="12" height="4" fill="#fff"/>`,
    CZ: `<rect width="30" height="10" fill="#fff"/><rect y="10" width="30" height="10" fill="#d7141a"/><path d="M0 0l15 10L0 20z" fill="#11457e"/>`,
    DE: rows(['#000', '#dd0000', '#ffce00']),
    DK: cross('#c8102e', '#fff'),
    EE: rows(['#0072ce', '#000', '#fff']),
    ES: `<rect width="30" height="20" fill="#aa151b"/><rect y="5" width="30" height="10" fill="#f1bf00"/>`,
    FI: cross('#fff', '#002f6c'),
    FR: cols(['#002395', '#fff', '#ed2939']),
    GB: `<rect width="30" height="20" fill="#012169"/><path d="M0 0l30 20M30 0L0 20" stroke="#fff" stroke-width="4"/><path d="M0 0l30 20M30 0L0 20" stroke="#c8102e" stroke-width="1.6"/><path d="M15 0v20M0 10h30" stroke="#fff" stroke-width="6.5"/><path d="M15 0v20M0 10h30" stroke="#c8102e" stroke-width="3.8"/>`,
    GE: `<rect width="30" height="20" fill="#fff"/><rect x="13" width="4" height="20" fill="#f00"/><rect y="8" width="30" height="4" fill="#f00"/>`,
    HK: `<rect width="30" height="20" fill="#de2910"/><circle cx="15" cy="10" r="4.5" fill="#fff"/><circle cx="15" cy="10" r="2" fill="#de2910"/>`,
    HU: rows(['#ce2939', '#fff', '#477050']),
    IE: cols(['#169b62', '#fff', '#ff883e']),
    IT: cols(['#009246', '#fff', '#ce2b37']),
    JP: `<rect width="30" height="20" fill="#fff"/><circle cx="15" cy="10" r="6" fill="#bc002d"/>`,
    KZ: `<rect width="30" height="20" fill="#00afca"/><circle cx="15" cy="9" r="4" fill="#fec50c"/>`,
    LT: rows(['#fdb913', '#006a44', '#c1272d']),
    LU: rows(['#ef3340', '#fff', '#00a3e0']),
    LV: `<rect width="30" height="20" fill="#9e3039"/><rect y="8" width="30" height="4" fill="#fff"/>`,
    MD: cols(['#0046ae', '#ffd200', '#cc092f']),
    NL: rows(['#ae1c28', '#fff', '#21468b']),
    NO: cross('#ba0c2f', '#fff', '#00205b'),
    PL: rows(['#fff', '#dc143c']),
    PT: `<rect width="30" height="20" fill="#f00"/><rect width="12" height="20" fill="#060"/><circle cx="12" cy="10" r="3.5" fill="#fc0"/>`,
    RO: cols(['#002b7f', '#fcd116', '#ce1126']),
    RS: rows(['#c6363c', '#0c4076', '#fff']),
    RU: rows(['#fff', '#0039a6', '#d52b1e']),
    SE: cross('#006aa7', '#fecc00'),
    SG: `<rect width="30" height="10" fill="#ef3340"/><rect y="10" width="30" height="10" fill="#fff"/><circle cx="7" cy="5" r="3" fill="#fff"/><circle cx="8.2" cy="5" r="3" fill="#ef3340"/>`,
    TR: `<rect width="30" height="20" fill="#e30a17"/><circle cx="12" cy="10" r="5" fill="#fff"/><circle cx="13.3" cy="10" r="4" fill="#e30a17"/><circle cx="18.3" cy="10" r="1.6" fill="#fff"/>`,
    UA: rows(['#0057b7', '#ffd700']),
    US: `<rect width="30" height="20" fill="#fff"/>${[0, 2, 4, 6, 8, 10, 12].map((i) => `<rect y="${i * 1.54}" width="30" height="1.54" fill="#b22234"/>`).join('')}<rect width="13" height="10.8" fill="#3c3b6e"/>`
  };

  const NAMES = {
    AE: 'ОАЭ', AM: 'Армения', AT: 'Австрия', BE: 'Бельгия', BG: 'Болгария', CA: 'Канада',
    CH: 'Швейцария', CZ: 'Чехия', DE: 'Германия', DK: 'Дания', EE: 'Эстония', ES: 'Испания',
    FI: 'Финляндия', FR: 'Франция', GB: 'Великобритания', GE: 'Грузия', HK: 'Гонконг',
    HU: 'Венгрия', IE: 'Ирландия', IT: 'Италия', JP: 'Япония', KZ: 'Казахстан', LT: 'Литва',
    LU: 'Люксембург', LV: 'Латвия', MD: 'Молдова', NL: 'Нидерланды', NO: 'Норвегия',
    PL: 'Польша', PT: 'Португалия', RO: 'Румыния', RS: 'Сербия', RU: 'Россия', SE: 'Швеция',
    SG: 'Сингапур', TR: 'Турция', UA: 'Украина', US: 'США'
  };

  // Названия стран, которые встречаются в подписках MS7 и Remnawave.
  const WORDS = [
    ['нидерланд', 'NL'], ['голланд', 'NL'], ['netherland', 'NL'], ['amsterdam', 'NL'],
    ['герман', 'DE'], ['german', 'DE'], ['frankfurt', 'DE'], ['deutsch', 'DE'],
    ['польш', 'PL'], ['poland', 'PL'], ['warsaw', 'PL'],
    ['великобритан', 'GB'], ['англи', 'GB'], ['britain', 'GB'], ['london', 'GB'], ['united kingdom', 'GB'],
    ['франц', 'FR'], ['france', 'FR'], ['paris', 'FR'],
    ['финлянд', 'FI'], ['finland', 'FI'], ['helsinki', 'FI'],
    ['швец', 'SE'], ['sweden', 'SE'], ['швейцар', 'CH'], ['switz', 'CH'],
    ['сша', 'US'], ['америк', 'US'], ['united states', 'US'], ['usa', 'US'],
    ['канад', 'CA'], ['canada', 'CA'], ['турц', 'TR'], ['turkey', 'TR'], ['istanbul', 'TR'],
    ['япон', 'JP'], ['japan', 'JP'], ['сингапур', 'SG'], ['singapore', 'SG'],
    ['казахст', 'KZ'], ['kazakh', 'KZ'], ['арм', 'AM'], ['armenia', 'AM'],
    ['груз', 'GE'], ['georgia', 'GE'], ['латв', 'LV'], ['latvia', 'LV'],
    ['литв', 'LT'], ['lithuania', 'LT'], ['эстон', 'EE'], ['estonia', 'EE'],
    ['австр', 'AT'], ['austria', 'AT'], ['чех', 'CZ'], ['czech', 'CZ'],
    ['испан', 'ES'], ['spain', 'ES'], ['итал', 'IT'], ['italy', 'IT'],
    ['россия', 'RU'], ['russia', 'RU'], ['москв', 'RU'],
    ['украин', 'UA'], ['ukraine', 'UA'], ['оаэ', 'AE'], ['dubai', 'AE'], ['emirat', 'AE'],
    ['гонконг', 'HK'], ['hong kong', 'HK'], ['норвег', 'NO'], ['norway', 'NO'],
    ['дан', 'DK'], ['denmark', 'DK'], ['бельг', 'BE'], ['belgium', 'BE'],
    ['румын', 'RO'], ['romania', 'RO'], ['молдов', 'MD'], ['moldova', 'MD'],
    ['серб', 'RS'], ['serbia', 'RS'], ['венгр', 'HU'], ['hungary', 'HU'],
    ['болгар', 'BG'], ['bulgaria', 'BG'], ['португал', 'PT'], ['portugal', 'PT'],
    ['ирланд', 'IE'], ['ireland', 'IE'], ['люксембург', 'LU'], ['luxembourg', 'LU']
  ];

  // Эмодзи-флаг состоит из двух региональных символов: 🇳 + 🇱 → NL.
  function fromEmoji(text) {
    const points = Array.from(text);
    for (let i = 0; i < points.length - 1; i++) {
      const a = points[i].codePointAt(0);
      const b = points[i + 1].codePointAt(0);
      if (a >= 0x1F1E6 && a <= 0x1F1FF && b >= 0x1F1E6 && b <= 0x1F1FF) {
        return String.fromCharCode(a - 0x1F1E6 + 65) + String.fromCharCode(b - 0x1F1E6 + 65);
      }
    }
    return '';
  }

  function countryCode(rawName) {
    const name = String(rawName || '');
    const emoji = fromEmoji(name);
    if (emoji && FLAGS[emoji]) return emoji;
    const lower = name.toLowerCase();
    for (const [word, code] of WORDS) {
      if (lower.includes(word)) return code;
    }
    const match = name.match(/(?:^|[^A-Za-z])([A-Z]{2})(?![A-Za-z])/);
    if (match && FLAGS[match[1]]) return match[1];
    return '';
  }

  // Имя ноды показываем как в подписке, только без эмодзи-флага:
  // в окне Windows он рисуется чёрно-белым квадратом.
  function cleanName(rawName) {
    return String(rawName || '')
      .replace(/[\u{1F1E6}-\u{1F1FF}]{2}/gu, '')
      .replace(/\s{2,}/g, ' ')
      .trim();
  }

  function flagHTML(rawName) {
    const code = countryCode(rawName);
    if (code && FLAGS[code]) {
      return `<span class="flag"><svg viewBox="0 0 30 20" preserveAspectRatio="none">${FLAGS[code]}</svg></span>`;
    }
    return `<span class="flag flag-empty">${code || '??'}</span>`;
  }

  window.MS7Flags = { flagHTML, countryCode, cleanName, countryName: (code) => NAMES[code] || code };
})();
