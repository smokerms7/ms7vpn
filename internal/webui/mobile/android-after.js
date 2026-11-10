/* MS7VPN для Android — после app.js. Подгоняет интерфейс ПК под телефон:
   нижнее меню с «Ещё», кнопка «Вставить» вместо Ctrl+V, тексты про телефон,
   скрытые Windows-настройки, системная кнопка «Назад». */
(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const native = window.MS7Native || null;
  const hide = (node) => { if (node) node.hidden = true; };
  const text = (node, value) => { if (node) node.textContent = value; };

  // ==== Windows-функции, которых на телефоне нет ====
  hide($('field-engine'));
  hide($('field-connection-mode'));
  hide($('open-folder'));
  for (const id of ['set-tray', 'set-consoleproxy', 'set-autostart']) {
    const label = $(id) && $(id).closest('label');
    hide(label);
    if (id === 'set-consoleproxy' && label && label.nextElementSibling && label.nextElementSibling.matches('small')) hide(label.nextElementSibling);
  }
  document.querySelectorAll('#set-theme ~ .field-hint').forEach((n) => text(n, 'Цвет применяется сразу.'));

  // ==== тексты ====
  const infoTitle = document.querySelector('.info-top .row-title');
  text(infoTitle, 'MS7VPN для Android');
  hide(document.querySelector('.add-sub .kbd-hint'));
  text($('split-apps-note'), 'Для режима «Вкл» в настройках');
  text(document.querySelector('#split-apps-card .split-tag'), 'Приложения через VPN');
  const appInput = $('app-input');
  if (appInput) appInput.placeholder = 'Пакет, например org.telegram.messenger';
  text($('proc-toggle'), 'Из установленных');
  const appsHint = document.querySelector('#split-apps-card > .field-hint');
  text(appsHint, 'Когда в настройках включено «Прокси для выбранных приложений», через VPN идут только эти приложения, остальные — напрямую. Изменения применятся при следующем подключении.');
  const ruHint = document.querySelector('.rule-help');
  if (ruHint) ruHint.innerHTML = ruHint.innerHTML.replace(' и с ядром Xray', '');

  // Частые вопросы — про телефон.
  const faq = document.querySelectorAll('.faq details');
  faq.forEach((item) => {
    const summary = item.querySelector('summary');
    const body = item.querySelector('p');
    const q = summary ? summary.textContent : '';
    if (q.startsWith('Чем отличаются VPN')) hide(item);
    if (q.startsWith('Как быстро подключиться')) text(body, 'Удерживайте большую кнопку секунду или нажмите «Самый быстрый» на вкладке «Серверы» — приложение проверит пинг всех серверов и подключится к самому быстрому.');
    if (q.startsWith('Как добавить подписку')) text(body, 'Скопируйте ссылку из бота и нажмите «Вставить» в разделе «Подписки». Или нажмите в боте кнопку «Открыть в приложении» — ссылка вида ms7vpn://add?url=… откроет MS7VPN и предложит добавить подписку.');
    if (q.startsWith('TUN или системный')) {
      text(summary, 'Android спрашивает разрешение на VPN');
      text(body, 'Это нормально: при первом подключении Android просит разрешить приложению создать VPN. Нажмите «ОК» — больше вопрос не появится. Значок ключа в строке состояния означает, что VPN включён.');
    }
    if (q.startsWith('Игра или торрент')) {
      text(summary, 'Банк или игра работают плохо через VPN');
      text(body, 'Включите «Российские сайты напрямую» в правилах маршрутизации. Или включите в настройках «Прокси для выбранных приложений» и оставьте в списке только нужные — остальные пойдут напрямую.');
    }
  });

  // ==== «Вставить» вместо Ctrl+V ====
  const addRow = document.querySelector('.add-sub .row');
  if (addRow && $('sub-url')) {
    const paste = document.createElement('button');
    paste.type = 'button';
    paste.className = 'btn btn-ghost m-paste';
    paste.textContent = 'Вставить';
    paste.addEventListener('click', async () => {
      let value = '';
      try { value = native && native.clipboard ? native.clipboard() : ''; } catch (_) { value = ''; }
      if (!value && navigator.clipboard && navigator.clipboard.readText) {
        try { value = await navigator.clipboard.readText(); } catch (_) { value = ''; }
      }
      value = (value || '').trim();
      if (!value) { $('sub-url').focus(); return; }
      $('sub-url').value = value;
      $('sub-add').click();
    });
    addRow.insertBefore(paste, $('sub-add'));
  }

  // ==== навигация как в Happ: без нижнего меню ====
  // Слева сверху — настройки (там же «Без VPN», подписки, статистика,
  // журнал, информация), справа — «+» для подписки. На главной — подписки с
  // серверами: выбор в одно касание, у каждой подписки тест пинга и обновление.
  const TOKEN = new URLSearchParams(location.search).get('token') || '';
  const navButton = (page) => document.querySelector(`.nav-item[data-page="${page}"]`);
  const openPage = (page) => {
    const b = navButton(page);
    if (b) b.click();
    const m = document.querySelector('.main');
    if (m) m.scrollTop = 0;
  };
  const currentPage = () => {
    const a = document.querySelector('.nav-item.is-active[data-page]');
    return a ? a.dataset.page : 'home';
  };
  const SUBPAGES = ['split', 'subs', 'stats', 'log', 'info'];
  const ICON = {
    gear: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3.2"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></svg>',
    back: '<svg viewBox="0 0 24 24"><path d="M15 5l-7 7 7 7"/></svg>',
    plus: '<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>',
    bolt: '<svg viewBox="0 0 24 24"><path d="M13 2L4 14h7l-1 8 9-12h-7z"/></svg>',
    sync: '<svg viewBox="0 0 24 24"><path d="M20 11a8 8 0 0 0-14.6-4.5M4 4v4h4"/><path d="M4 13a8 8 0 0 0 14.6 4.5M20 20v-4h-4"/></svg>',
    chev: '<svg viewBox="0 0 24 24"><path d="M6 9l6 6 6-6"/></svg>',
    go: '<svg viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></svg>',
    trash: '<svg viewBox="0 0 24 24"><path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/></svg>'
  };

  const topbar = document.querySelector('.topbar');
  const leftBtn = document.createElement('button');
  leftBtn.type = 'button';
  leftBtn.className = 'm-top-btn';
  topbar.insertBefore(leftBtn, topbar.firstChild);
  // Вместо приветствия — название, как в Happ.
  const brand = document.createElement('div');
  brand.className = 'm-brand';
  brand.innerHTML = '<img src="logo.png" alt="MS7" class="m-brand-logo">';
  leftBtn.after(brand);
  const addBtn = document.createElement('button');
  addBtn.type = 'button';
  addBtn.className = 'm-top-btn m-top-add';
  addBtn.setAttribute('aria-label', 'Добавить подписку');
  addBtn.innerHTML = ICON.plus;
  topbar.appendChild(addBtn);
  leftBtn.addEventListener('click', () => {
    if (window.MS7SettingsBack && window.MS7SettingsBack()) return;
    const page = currentPage();
    if (page === 'home') openPage('settings');
    else if (SUBPAGES.includes(page)) openPage('settings');
    else openPage('home');
  });
  const PLUS_ICONS = {
    url: '<svg viewBox="0 0 24 24"><path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/></svg>',
    paste: '<svg viewBox="0 0 24 24"><rect x="6" y="4" width="12" height="17" rx="2"/><path d="M9 4V3h6v1M9 10h6M9 14h6M9 18h3"/></svg>',
    qr: '<svg viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><path d="M14 14h3v3M21 14v.01M14 21h3M21 18v3h-3"/></svg>',
    edit: '<svg viewBox="0 0 24 24"><path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13 7l4 4"/></svg>'
  };
  const pop = document.createElement('div');
  pop.className = 'm-pop';
  pop.hidden = true;
  pop.innerHTML = [
    ['url', 'URL подписки'],
    ['paste', 'Вставить из буфера обмена'],
    ['qr', 'QR-код'],
    ['edit', 'Ручной ввод']
  ].map(([id, title]) => `<button type="button" class="m-pop-item" data-plus="${id}">${PLUS_ICONS[id]}<span>${title}</span></button>`).join('');
  document.body.appendChild(pop);
  const closePop = () => { pop.classList.remove('is-open'); setTimeout(() => { pop.hidden = true; }, 140); };
  const openPop = () => {
    const r = addBtn.getBoundingClientRect();
    pop.style.top = (r.bottom + 8) + 'px';
    pop.style.right = (window.innerWidth - r.right) + 'px';
    pop.hidden = false;
    requestAnimationFrame(() => pop.classList.add('is-open'));
  };
  addBtn.addEventListener('click', (event) => { event.stopPropagation(); if (pop.hidden) openPop(); else closePop(); });
  document.addEventListener('click', (event) => { if (!pop.hidden && !pop.contains(event.target)) closePop(); }, true);

  // Небольшое окно ввода (ссылка подписки или сервера).
  const dlg = document.createElement('div');
  dlg.className = 'm-dlg';
  dlg.hidden = true;
  dlg.innerHTML = `<div class="m-dlg-box">
      <h3 class="m-dlg-title"></h3>
      <textarea class="m-dlg-url" rows="3" spellcheck="false"></textarea>
      <input type="text" class="m-dlg-name" spellcheck="false" placeholder="Название (необязательно)">
      <div class="m-dlg-actions">
        <button type="button" class="btn btn-ghost" data-dlg="paste">Вставить</button>
        <button type="button" class="btn btn-ghost" data-dlg="cancel">Отмена</button>
        <button type="button" class="btn btn-primary" data-dlg="ok">Добавить</button>
      </div>
    </div>`;
  document.body.appendChild(dlg);
  const dlgUrl = dlg.querySelector('.m-dlg-url');
  const dlgName = dlg.querySelector('.m-dlg-name');
  let dlgMode = 'url';
  const closeDlg = () => { dlg.hidden = true; };
  const openDlg = (mode) => {
    dlgMode = mode;
    dlg.querySelector('.m-dlg-title').textContent = mode === 'url' ? 'URL подписки' : 'Ручной ввод';
    dlgUrl.placeholder = mode === 'url' ? 'Ссылка https://… из Telegram-бота' : 'Ссылка сервера vless://, vmess://, trojan://, ss://, hy2://';
    dlgName.hidden = mode === 'url';
    dlgUrl.value = '';
    dlgName.value = '';
    dlg.hidden = false;
    setTimeout(() => dlgUrl.focus(), 60);
  };
  const clipboardText = async () => {
    let value = '';
    try { value = native && native.clipboard ? native.clipboard() : ''; } catch (_) { value = ''; }
    if (!value && navigator.clipboard && navigator.clipboard.readText) {
      try { value = await navigator.clipboard.readText(); } catch (_) { value = ''; }
    }
    return (value || '').trim();
  };
  async function addLink(url, name) {
    url = (url || '').trim();
    if (!url) { say(dlgMode === 'edit' ? 'Вставьте ссылку сервера' : 'Вставьте ссылку подписки', 'err'); return false; }
    say('Добавляем…');
    try {
      const state = await call('/api/subscriptions/add', { url, name: (name || '').trim() });
      say(/^https?:/i.test(url) ? 'Подписка добавлена' : 'Сервер добавлен', 'ok');
      last = '';
      renderSubs(state);
      openPage('home');
      return true;
    } catch (error) {
      say(error.message, 'err');
      return false;
    }
  }
  dlg.addEventListener('click', async (event) => {
    if (event.target === dlg) { closeDlg(); return; }
    const act = event.target.closest('[data-dlg]');
    if (!act) return;
    if (act.dataset.dlg === 'cancel') closeDlg();
    if (act.dataset.dlg === 'paste') { const t = await clipboardText(); if (t) dlgUrl.value = t; else say('Буфер обмена пуст', 'err'); }
    if (act.dataset.dlg === 'ok' && await addLink(dlgUrl.value, dlgName.hidden ? '' : dlgName.value)) closeDlg();
  });
  window.MS7OnQr = (text) => { if (text) addLink(text, ''); };
  pop.addEventListener('click', async (event) => {
    const item = event.target.closest('[data-plus]');
    if (!item) return;
    closePop();
    const what = item.dataset.plus;
    if (what === 'url') openDlg('url');
    if (what === 'edit') openDlg('edit');
    if (what === 'paste') {
      const t = await clipboardText();
      if (!t) { say('Буфер обмена пуст', 'err'); return; }
      addLink(t, '');
    }
    if (what === 'qr') {
      try { if (native && native.scanQr) { native.scanQr(); return; } } catch (_) { /* нет сканера */ }
      say('Сканер QR-кода недоступен', 'err');
    }
  });
  window.MS7ClosePlus = () => {
    if (!pop.hidden) { closePop(); return true; }
    if (!dlg.hidden) { closeDlg(); return true; }
    return false;
  };

  // Меню в начале настроек: разделы, которых больше нет внизу.
  const hub = document.createElement('div');
  hub.className = 'card m-hub';
  const hubItems = [
    ['split', 'Без VPN', 'Сайты и приложения мимо туннеля'],
    ['subs', 'Подписки', 'Добавить, переименовать порядок, удалить'],
    ['stats', 'Статистика', 'Трафик и время в VPN'],
    ['log', 'Журнал', 'События и ошибки'],
    ['info', 'О приложении', 'Версия, устройство, обновления, помощь']
  ];
  hub.innerHTML = hubItems.map(([page, title, sub]) => {
    const source = navButton(page);
    const icon = source && source.querySelector('svg') ? source.querySelector('svg').outerHTML : '';
    return `<button type="button" class="m-hub-row" data-go="${page}">${icon}<span class="row-main"><span class="row-title">${title}</span><span class="row-sub">${sub}</span></span><i class="m-hub-go">${ICON.go}</i></button>`;
  }).join('');
  hub.addEventListener('click', (event) => {
    const row = event.target.closest('[data-go]');
    if (row) openPage(row.dataset.go);
  });
  const settingsHead = document.querySelector('#page-settings .page-head');
  settingsHead.parentNode.insertBefore(hub, settingsHead.nextSibling);

  const syncTop = () => {
    const page = currentPage();
    document.body.dataset.mPage = page;
    leftBtn.innerHTML = page === 'home' ? ICON.gear : ICON.back;
    leftBtn.setAttribute('aria-label', page === 'home' ? 'Настройки' : 'Назад');
  };
  const items = [...document.querySelectorAll('.nav-item[data-page]')];
  const observer = new MutationObserver(syncTop);
  items.forEach((b) => observer.observe(b, { attributes: true, attributeFilter: ['class'] }));
  syncTop();

  // ==== подписки и серверы прямо на главной ====
  const flags = window.MS7Flags;
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const call = async (path, body) => {
    const response = await fetch(path, {
      method: body ? 'POST' : 'GET',
      headers: Object.assign({ 'X-MS7-Token': TOKEN }, body ? { 'Content-Type': 'application/json' } : {}),
      body: body ? JSON.stringify(body) : undefined
    });
    const payload = await response.json().catch(() => null);
    if (!response.ok || !payload || payload.ok !== true) {
      throw new Error((payload && payload.error && payload.error.message) || 'Не удалось выполнить запрос');
    }
    return payload.data;
  };
  const say = (message, kind) => {
    const node = $('toast');
    node.textContent = message;
    node.className = 'toast' + (kind ? ' is-' + kind : '');
    node.hidden = false;
    clearTimeout(node._timer);
    node._timer = setTimeout(() => { node.hidden = true; }, 2200);
  };
  const gb = (bytes) => (bytes / 1073741824).toLocaleString('ru-RU', { maximumFractionDigits: 1 }) + ' ГБ';
  const subLine = (sub) => {
    const info = sub.userInfo || {};
    const parts = [];
    const used = (info.uploadBytes || 0) + (info.downloadBytes || 0);
    parts.push(info.totalBytes ? `${gb(used)} из ${gb(info.totalBytes)}` : `${gb(used)} · безлимит`);
    if (info.expireUnix) {
      const days = Math.ceil((info.expireUnix * 1000 - Date.now()) / 86400000);
      parts.push(days > 0 ? `ещё ${days} дн.` : 'истекла');
    }
    const count = (sub.nodes || []).length;
    parts.push(`${count} ${count % 10 === 1 && count % 100 !== 11 ? 'сервер' : (count % 10 >= 2 && count % 10 <= 4 && (count % 100 < 10 || count % 100 >= 20) ? 'сервера' : 'серверов')}`);
    return parts.join(' · ');
  };
  const pingIcons = () => { try { return localStorage.getItem('m-ping-display') === 'icons'; } catch (_) { return false; } };
  const bars = (level, cls) => `<span class="m-ping m-bars ${cls}">${[1, 2, 3].map((i) => `<i class="${i <= level ? 'on' : ''}"></i>`).join('')}</span>`;
  const pingBadge = (node) => {
    if (pingIcons()) {
      if (node.pingMs > 0) return node.pingMs < 150 ? bars(3, 'is-fast') : node.pingMs < 400 ? bars(2, 'is-mid') : bars(1, 'is-bad');
      if (node.pingStatus && node.pingStatus !== 'ok') return bars(0, 'is-bad');
      return bars(0, '');
    }
    if (node.pingMs > 0) {
      const cls = node.pingMs < 150 ? 'is-fast' : node.pingMs < 400 ? 'is-mid' : 'is-bad';
      return `<span class="m-ping ${cls}">${node.pingMs} мс</span>`;
    }
    if (node.pingStatus && node.pingStatus !== 'ok') return '<span class="m-ping is-bad">нет связи</span>';
    return '<span class="m-ping">—</span>';
  };

  const box = document.createElement('div');
  box.className = 'm-subs';
  const homeCol = document.querySelector('#page-home .home-col');
  homeCol.insertBefore(box, $('home-notice'));
  let collapsed = {};
  try { collapsed = JSON.parse(localStorage.getItem('m-collapsed2') || '{}'); } catch (_) { collapsed = {}; }
  const busy = {};
  let last = '';
  let snapshot = null;

  function renderSubs(state) {
    snapshot = state;
    // Пока палец тянет карточку, не перерисовываем — иначе свайп оборвётся.
    if (drag && drag.axis) return;
    const subs = state.subscriptions || [];
    const conn = state.connection || {};
    const key = JSON.stringify([
      subs.map((s) => [s.id, s.name, s.userInfo, s.lastError, (s.nodes || []).map((n) => [n.id, n.name, n.pingMs, n.pingStatus, n.supported])]),
      state.selectedNodeId, conn.nodeId, conn.connected, conn.connecting, collapsed, busy, state.nodeNames
    ]);
    if (key === last) return;
    last = key;
    if (!subs.length) {
      box.innerHTML = `<button type="button" class="card m-empty" data-add><span class="m-empty-ic">${ICON.plus}</span><span class="row-main"><span class="row-title">Добавьте подписку</span><span class="row-sub">Ссылка из Telegram-бота — кнопка «+» вверху</span></span></button>`;
      return;
    }
    const names = state.nodeNames || {};
    box.innerHTML = subs.map((sub) => {
      const nodes = sub.nodes || [];
      const hasSelected = nodes.some((n) => n.id === state.selectedNodeId);
      const open = !collapsed[sub.id]; // по умолчанию все серверы видны сразу
      const rows = open ? nodes.map((node) => {
        const name = names[node.id] || node.name;
        const selected = node.id === state.selectedNodeId;
        const live = selected && conn.connected;
        return `<button type="button" class="m-node${selected ? ' is-selected' : ''}${live ? ' is-live' : ''}${node.supported === false ? ' is-off' : ''}" data-node="${esc(node.id)}">
          <span class="m-flag">${flags ? flags.flagHTML(name) : ''}</span>
          <span class="m-node-name">${esc(flags ? flags.cleanName(name) : name)}</span>
          ${pingBadge(node)}
          <span class="m-radio"></span>
        </button>`;
      }).join('') : '';
      return `<div class="m-swipe${swiped === sub.id ? ' is-open' : ''}" data-swipe="${esc(sub.id)}">
        <button type="button" class="m-swipe-del" data-delete="${esc(sub.id)}">${ICON.trash}<span>Удалить</span></button>
        <div class="card m-sub${hasSelected ? ' has-selected' : ''}" data-sub="${esc(sub.id)}">
        <div class="m-sub-head">
          <button type="button" class="m-sub-title" data-toggle="${esc(sub.id)}">
            <span class="row-main"><span class="row-title">${esc(sub.name || 'Подписка')}</span><span class="row-sub">${esc(subLine(sub))}</span></span>
            <i class="m-chev${open ? ' is-open' : ''}">${ICON.chev}</i>
          </button>
          <button type="button" class="m-icon-btn${busy['ping' + sub.id] ? ' is-busy' : ''}" data-ping="${esc(sub.id)}" aria-label="Тест пинга" title="Тест пинга">${ICON.bolt}</button>
          <button type="button" class="m-icon-btn${busy['ref' + sub.id] ? ' is-busy' : ''}" data-refresh="${esc(sub.id)}" aria-label="Обновить подписку" title="Обновить подписку">${ICON.sync}</button>
        </div>
        ${sub.lastError ? `<p class="m-sub-err">${esc(sub.lastError)}</p>` : ''}
        ${open ? `<div class="m-nodes">${rows || '<span class="events-empty">Серверов нет — обновите подписку</span>'}</div>` : ''}
        </div>
      </div>`;
    }).join('');
  }

  // ==== свайп справа налево — удалить подписку ====
  // Карточка уезжает влево, справа открывается красная «Удалить». Удаляется
  // только по нажатию на неё; свайп вправо или касание в стороне — закрыть.
  const SWIPE_OPEN = 104;
  let swiped = null;
  let drag = null;
  let dragging = false;
  const cardOf = (wrap) => wrap && wrap.querySelector('.m-sub');
  const setShift = (wrap, px, animate) => {
    const card = cardOf(wrap);
    if (!card) return;
    card.style.transition = animate ? 'transform 180ms ease' : 'none';
    card.style.transform = px ? `translateX(${-px}px)` : '';
    wrap.classList.toggle('is-revealing', px > 0);
  };
  const closeSwipe = () => {
    if (!swiped) return;
    const wrap = box.querySelector(`.m-swipe[data-swipe="${CSS.escape(swiped)}"]`);
    swiped = null;
    if (wrap) { wrap.classList.remove('is-open'); setShift(wrap, 0, true); }
  };
  // Pointer-события и touch-action: pan-y — вертикальную прокрутку ведёт сам
  // браузер, без задержек, а горизонтальный жест достаётся свайпу.
  box.addEventListener('pointerdown', (event) => {
    if (event.pointerType === 'mouse' && event.button !== 0) return;
    const wrap = event.target.closest('.m-swipe');
    if (!wrap) return;
    if (swiped && swiped !== wrap.dataset.swipe) closeSwipe();
    drag = { wrap, id: event.pointerId, x: event.clientX, y: event.clientY, base: wrap.classList.contains('is-open') ? SWIPE_OPEN : 0, dx: 0, axis: '' };
  }, { passive: true });
  box.addEventListener('pointermove', (event) => {
    if (!drag || event.pointerId !== drag.id) return;
    const dx = drag.x - event.clientX; // > 0 — палец идёт влево
    const dy = event.clientY - drag.y;
    if (!drag.axis) {
      if (Math.abs(dx) < 10 && Math.abs(dy) < 10) return;
      drag.axis = Math.abs(dx) > Math.abs(dy) * 1.2 ? 'x' : 'y';
      if (drag.axis === 'y') { drag = null; return; }
    }
    dragging = true;
    drag.dx = dx;
    const shift = Math.max(0, Math.min(drag.base + dx, drag.wrap.offsetWidth * 0.6));
    setShift(drag.wrap, shift, false);
  }, { passive: true });
  const endDrag = () => {
    if (!drag) return;
    const { wrap, base, dx, axis } = drag;
    drag = null;
    if (axis !== 'x') { dragging = false; return; }
    const shift = base + dx;
    if (shift > SWIPE_OPEN * 0.6) {
      swiped = wrap.dataset.swipe;
      wrap.classList.add('is-open');
      setShift(wrap, SWIPE_OPEN, true);
    } else {
      if (swiped === wrap.dataset.swipe) swiped = null;
      wrap.classList.remove('is-open');
      setShift(wrap, 0, true);
    }
    // Клик, который придёт после свайпа, не должен выбрать сервер.
    setTimeout(() => { dragging = false; }, 60);
  };
  box.addEventListener('pointerup', endDrag);
  box.addEventListener('pointercancel', endDrag);
  box.addEventListener('click', (event) => {
    if (dragging) { event.stopImmediatePropagation(); event.preventDefault(); return; }
    const del = event.target.closest('[data-delete]');
    if (del) {
      event.stopImmediatePropagation();
      const id = del.dataset.delete;
      const wrap = del.closest('.m-swipe');
      if (wrap) wrap.classList.add('is-removing');
      call('/api/subscriptions/delete', { id }).then((state) => {
        swiped = null;
        say('Подписка удалена', 'ok');
        last = '';
        renderSubs(state);
      }).catch((error) => {
        if (wrap) wrap.classList.remove('is-removing');
        say(error.message, 'err');
      });
      return;
    }
    // Открыта «Удалить», а коснулись карточки — сначала просто закрываем.
    if (swiped && event.target.closest('.m-swipe.is-open')) {
      event.stopImmediatePropagation();
      closeSwipe();
    }
  }, true);
  document.addEventListener('pointerdown', (event) => {
    if (swiped && !event.target.closest('.m-swipe.is-open')) closeSwipe();
  }, { passive: true });

  async function poll() {
    try { renderSubs(await call('/api/state')); } catch (_) { /* бэкенд перезапускается */ }
  }
  window.MS7RerenderSubs = () => { last = ''; if (snapshot) renderSubs(snapshot); };
  setInterval(poll, 2000);
  poll();

  box.addEventListener('click', async (event) => {
    if (event.target.closest('[data-add]')) { addBtn.click(); return; }
    const toggle = event.target.closest('[data-toggle]');
    if (toggle) {
      const id = toggle.dataset.toggle;
      const card = toggle.closest('.m-sub');
      collapsed[id] = !!(card && card.querySelector('.m-nodes'));
      try { localStorage.setItem('m-collapsed2', JSON.stringify(collapsed)); } catch (_) { /* не запомним */ }
      last = '';
      if (snapshot) renderSubs(snapshot);
      return;
    }
    const ping = event.target.closest('[data-ping]');
    const refresh = event.target.closest('[data-refresh]');
    if (ping || refresh) {
      const id = ping ? ping.dataset.ping : refresh.dataset.refresh;
      const tag = (ping ? 'ping' : 'ref') + id;
      if (busy[tag]) return;
      busy[tag] = true;
      last = '';
      if (snapshot) renderSubs(snapshot);
      try {
        const state = ping
          ? await call('/api/nodes/ping', { subscriptionId: id, nodeIds: [] })
          : await call('/api/subscriptions/refresh', { id });
        say(ping ? 'Пинг проверен' : 'Подписка обновлена', 'ok');
        delete busy[tag];
        last = '';
        renderSubs(state);
      } catch (error) {
        delete busy[tag];
        last = '';
        if (snapshot) renderSubs(snapshot);
        say(error.message, 'err');
      }
      return;
    }
    const row = event.target.closest('[data-node]');
    if (!row || row.classList.contains('is-off')) return;
    const nodeId = row.dataset.node;
    try {
      const state = await call('/api/nodes/select', { nodeId });
      last = '';
      renderSubs(state);
      const conn = state.connection || {};
      // Подключены к другому серверу — переключаемся сразу.
      if (conn.connected && conn.nodeId !== nodeId) {
        say('Переключаемся…');
        await call('/api/connect', { nodeId, mode: '' });
        poll();
      }
    } catch (error) {
      say(error.message, 'err');
    }
  });

  // ==== системная «Назад» (вызывает оболочка) ====
  window.MS7Back = () => {
    if (window.MS7ClosePlus && window.MS7ClosePlus()) return true;
    if (window.MS7SettingsBack && window.MS7SettingsBack()) return true;
    for (const [modal, close] of [['srv-modal', 'srv-close'], ['zapret-modal', 'zapret-close']]) {
      if ($(modal) && !$(modal).hidden) { $(close).click(); return true; }
    }
    if (!$('link-bar').hidden) { $('link-bar-no').click(); return true; }
    const page = currentPage();
    if (page !== 'home') { openPage(SUBPAGES.includes(page) ? 'settings' : 'home'); return true; }
    return false;
  };

  // Цвет строки состояния Android — под выбранное оформление.
  const syncBars = () => {
    try { if (native && native.theme) native.theme(document.documentElement.getAttribute('data-theme') || 'brand'); } catch (_) { /* старое окно */ }
  };
  new MutationObserver(syncBars).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  syncBars();

  // Высота экранной клавиатуры: поле ввода не должно прятаться под ней.
  document.addEventListener('focusin', (event) => {
    const target = event.target;
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) {
      setTimeout(() => target.scrollIntoView({ block: 'center', behavior: 'smooth' }), 250);
    }
  });
})();

// ==== настройки как в Happ: одна страница, разделы, всё видно сразу ====
// На телефоне только VPN — «Запрета» нет. Всё сохраняется сразу.
(() => {
  const $ = (id) => document.getElementById(id);
  const page = $('page-settings');
  if (!page) return;
  const TOKEN = new URLSearchParams(location.search).get('token') || '';
  const call = async (path, body) => {
    const response = await fetch(path, {
      method: body ? 'POST' : 'GET',
      headers: Object.assign({ 'X-MS7-Token': TOKEN }, body ? { 'Content-Type': 'application/json' } : {}),
      body: body ? JSON.stringify(body) : undefined
    });
    const payload = await response.json().catch(() => null);
    if (!response.ok || !payload || payload.ok !== true) throw new Error((payload && payload.error && payload.error.message) || 'Не удалось выполнить запрос');
    return payload.data;
  };
  const say = (message, kind) => {
    const node = $('toast');
    node.textContent = message;
    node.className = 'toast' + (kind ? ' is-' + kind : '');
    node.hidden = false;
    clearTimeout(node._timer);
    node._timer = setTimeout(() => { node.hidden = true; }, 2000);
  };
  const go = (target, focusId) => {
    const b = document.querySelector(`.nav-item[data-page="${target}"]`);
    if (b) b.click();
    const m = document.querySelector('.main');
    if (m) m.scrollTop = 0;
    if (focusId && $(focusId)) setTimeout(() => $(focusId).scrollIntoView({ block: 'start', behavior: 'smooth' }), 120);
  };
  const chevron = '<i class="m-hub-go"><svg viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></svg></i>';
  const el = (html) => { const t = document.createElement('template'); t.innerHTML = html.trim(); return t.content.firstElementChild; };
  const linkRow = (title, sub, onClick) => {
    const row = el(`<button type="button" class="m-row m-link-row"><span class="row-main"><span class="row-title">${title}</span>${sub ? `<span class="row-sub">${sub}</span>` : ''}</span>${chevron}</button>`);
    row.addEventListener('click', onClick);
    return row;
  };
  const pick = (id, selector) => { const n = $(id); return n ? (selector ? n.closest(selector) : n) : null; };

  // Свои поля телефона сохраняем сами: берём свежие настройки и меняем только своё.
  async function saveExtra(patch) {
    try {
      const state = await call('/api/state');
      await call('/api/settings', Object.assign({}, state.settings, patch));
      say('Настройки сохранены', 'ok');
    } catch (error) {
      say(error.message, 'err');
    }
  }
  const toggle = (id, title, sub) => el(`<label class="switch m-toggle"><input type="checkbox" id="${id}"><span>${title}${sub ? `<small class="m-sub-hint">${sub}</small>` : ''}</span></label>`);

  // ---- новые пункты ----
  const refreshOnStart = toggle('m-refresh-start', 'Обновлять подписки при запуске');
  const sniffing = toggle('m-sniffing', 'Сниффинг', 'Определять сайт в трафике — нужно для правил по доменам');
  const allowLan = toggle('m-allow-lan', 'Разрешить подключения из LAN', 'Другие устройства в сети смогут ходить через телефон: SOCKS5 :10808, HTTP :10809');
  const perApp = el(`<div class="field m-perapp"><span>Прокси для выбранных приложений</span>
      <div class="chips" id="m-perapp"><button type="button" class="chip" data-mode="off">Выкл</button><button type="button" class="chip" data-mode="on">Вкл</button></div>
      <small class="field-hint" id="m-perapp-hint"></small></div>`);
  const perAppHints = {
    off: 'Через VPN идут все приложения.',
    on: 'Через VPN идут только выбранные приложения, остальные — напрямую.'
  };
  const appsLink = linkRow('Выбрать приложения', 'Какие приложения пойдут через VPN в режиме «Вкл»', () => go('split', 'split-apps-card'));
  const mtu = el(`<label class="field"><span>MTU туннеля</span><input type="text" inputmode="numeric" id="m-mtu" placeholder="1500" spellcheck="false"><small class="field-hint">От 1280 до 9000. Если сайты грузятся не до конца — уменьшите до 1400.</small></label>`);
  const routing = linkRow('Правила маршрутизации', 'Российские сайты напрямую, свои списки сайтов', () => go('split'));
  const useRouting = toggle('m-routing', 'Использовать маршрутизацию', 'Выключите — весь трафик пойдёт через VPN без исключений');
  const hwid = toggle('m-hwid', 'Отправлять HWID', 'Идентификатор устройства для лимита устройств в подписке');
  const mux = toggle('m-mux', 'Mux', 'Несколько соединений в одном — быстрее открываются сайты. Не работает с XTLS Vision и XHTTP');
  const fragment = toggle('m-fragment', 'Фрагментация', 'Дробит начало TLS-соединения — помогает, если провайдер режет VPN');
  const autostart = toggle('m-autostart', 'Запускать при включении телефона', 'Сразу подключаться к VPN после перезагрузки');
  const autoType = el(`<div class="field"><span>Автоподключение к серверу</span><div class="chips" id="m-autotype"><button type="button" class="chip" data-auto="last">Последний</button><button type="button" class="chip" data-auto="fastest">Самый быстрый</button></div></div>`);
  const strategy = el(`<label class="field"><span>Стратегия доменов</span><select id="m-strategy"><option value="IPIfNonMatch">IPIfNonMatch — по умолчанию</option><option value="AsIs">AsIs — только домены</option><option value="IPOnDemand">IPOnDemand — сразу по IP</option></select></label>`);
  const pingView = el(`<div class="field"><span>Отображение пинга</span><div class="chips" id="m-pingview"><button type="button" class="chip" data-view="time">Время</button><button type="button" class="chip" data-view="icons">Значки</button></div></div>`);
  const battery = linkRow('Работа в фоне', 'Отключить оптимизацию батареи, чтобы Android не обрывал VPN', () => { try { window.MS7Native.batterySettings(); } catch (_) { /* нет оболочки */ } });
  const alwaysOn = linkRow('Постоянный VPN', 'Системные настройки Android: «Постоянная VPN» и «Блокировать без VPN»', () => { try { window.MS7Native.vpnSettings(); } catch (_) { /* нет оболочки */ } });
  const bot = linkRow('Telegram-бот', 'Подписка, оплата и поддержка', () => { const b = document.querySelector('[data-bot="buy"]'); if (b) b.click(); });
  const channel = linkRow('Telegram-канал', 'Новости MS7VPN', () => { if ($('open-channel')) $('open-channel').click(); });

  function langField() {
    const current = window.MS7Lang === 'en' ? 'en' : 'ru';
    const field = el(`<div class="field"><span>Язык</span><div class="chips"><button type="button" class="chip${current === 'ru' ? ' is-on' : ''}" data-lang="ru">Русский</button><button type="button" class="chip${current === 'en' ? ' is-on' : ''}" data-lang="en">English</button></div></div>`);
    field.addEventListener('click', (event) => {
      const chip = event.target.closest('[data-lang]');
      if (chip && chip.dataset.lang !== current && window.MS7SetLang) window.MS7SetLang(chip.dataset.lang);
    });
    return field;
  }

  const pingGrid = pick('set-pingmode', '.grid-2');
  const pingHint = pingGrid && pingGrid.nextElementSibling && pingGrid.nextElementSibling.matches('small') ? pingGrid.nextElementSibling : null;
  if (pingHint) pingHint.textContent = '«Через сервер» — через каждый сервер открывается адрес проверки: так видно настоящую скорость отклика. TCP проверяет только, открыт ли порт.';
  const resetRow = pick('settings-reset');
  if (resetRow) resetRow.classList.add('m-reset');

  const sections = [
    ['Подписки', [
      pick('set-autorefresh', 'label'),
      refreshOnStart,
      pick('set-autoping', 'label'),
      pick('set-autoconnect', 'label'),
      autoType,
      pick('set-sort', '.field'),
      hwid
    ]],
    ['Настройки туннеля', [
      perApp,
      appsLink,
      useRouting,
      routing,
      pick('field-blockads'),
      pick('field-dns-presets'),
      pick('field-dns'),
      strategy,
      mtu
    ]],
    ['Расширенные настройки', [
      pingGrid,
      pingHint,
      pick('set-testurl', '.field'),
      pingView,
      sniffing,
      mux,
      fragment,
      allowLan
    ]],
    ['Интерфейс', [
      pick('set-theme', '.field'),
      langField(),
      pick('font-size', '.field')
    ]],
    ['Другие настройки', [
      autostart,
      battery,
      alwaysOn,
      linkRow('Статистика', 'Трафик и время в VPN', () => go('stats')),
      linkRow('Журнал', 'События и ошибки', () => go('log')),
      linkRow('Подписки', 'Порядок, удаление, бесплатная подписка', () => go('subs')),
      bot,
      channel,
      linkRow('О приложении', 'Версия, устройство, обновления, помощь', () => go('info')),
      resetRow
    ]]
  ];

  const oldCards = [...page.querySelectorAll('.card.settings-card')];
  let after = page.querySelector('.page-head');
  for (const [title, nodes] of sections) {
    const head = el(`<div class="m-sec-title">${title}</div>`);
    const card = el('<div class="card settings-card m-sec"></div>');
    for (const node of nodes) if (node) card.appendChild(node);
    after.after(head);
    head.after(card);
    after = card;
  }
  // В старых карточках остались только скрытые Windows-поля.
  for (const card of oldCards) card.hidden = true;
  const oldHub = page.querySelector('.m-hub');
  if (oldHub) oldHub.remove();

  // ---- значения новых пунктов ----
  function fill(settings) {
    $('m-refresh-start').checked = !!settings.refreshOnStart;
    $('m-sniffing').checked = !settings.noSniffing;
    $('m-allow-lan').checked = !!settings.allowLan;
    const mode = settings.perAppMode === 'on' ? 'on' : 'off';
    for (const chip of perApp.querySelectorAll('[data-mode]')) chip.classList.toggle('is-on', chip.dataset.mode === mode);
    $('m-perapp-hint').textContent = perAppHints[mode];
    if (document.activeElement !== $('m-mtu')) $('m-mtu').value = settings.tunMtu ? String(settings.tunMtu) : '';
    $('m-routing').checked = !settings.noRouting;
    $('m-hwid').checked = !settings.noHwid;
    $('m-mux').checked = !!settings.mux;
    $('m-fragment').checked = !!settings.fragment;
    $('m-strategy').value = settings.domainStrategy || 'IPIfNonMatch';
    const auto = settings.autoConnectType === 'fastest' ? 'fastest' : 'last';
    for (const chip of autoType.querySelectorAll('[data-auto]')) chip.classList.toggle('is-on', chip.dataset.auto === auto);
    let view = 'time';
    try { view = localStorage.getItem('m-ping-display') || 'time'; } catch (_) { /* по умолчанию */ }
    for (const chip of pingView.querySelectorAll('[data-view]')) chip.classList.toggle('is-on', chip.dataset.view === view);
    call('/api/autostart').then((d) => { $('m-autostart').checked = !!(d && d.enabled); }).catch(() => {});
    const batteryOk = (() => { try { return window.MS7Native.batteryUnrestricted(); } catch (_) { return false; } })();
    const sub = battery.querySelector('.row-sub');
    if (sub) sub.textContent = batteryOk ? 'Оптимизация батареи уже отключена — VPN не будет обрываться' : 'Отключить оптимизацию батареи, чтобы Android не обрывал VPN';
  }
  const load = () => call('/api/state').then((state) => fill(state.settings || {})).catch(() => {});
  load();
  new MutationObserver(() => { if (page.classList.contains('is-active')) load(); }).observe(page, { attributes: true, attributeFilter: ['class'] });

  $('m-refresh-start').addEventListener('change', (e) => saveExtra({ refreshOnStart: e.target.checked }));
  $('m-sniffing').addEventListener('change', (e) => saveExtra({ noSniffing: !e.target.checked }));
  $('m-allow-lan').addEventListener('change', (e) => saveExtra({ allowLan: e.target.checked }));
  perApp.addEventListener('click', (event) => {
    const chip = event.target.closest('[data-mode]');
    if (!chip) return;
    for (const c of perApp.querySelectorAll('[data-mode]')) c.classList.toggle('is-on', c === chip);
    $('m-perapp-hint').textContent = perAppHints[chip.dataset.mode];
    saveExtra({ perAppMode: chip.dataset.mode });
  });
  $('m-routing').addEventListener('change', (e) => saveExtra({ noRouting: !e.target.checked }));
  $('m-hwid').addEventListener('change', (e) => saveExtra({ noHwid: !e.target.checked }));
  $('m-mux').addEventListener('change', (e) => saveExtra({ mux: e.target.checked }));
  $('m-fragment').addEventListener('change', (e) => saveExtra({ fragment: e.target.checked }));
  $('m-strategy').addEventListener('change', (e) => saveExtra({ domainStrategy: e.target.value }));
  $('m-autostart').addEventListener('change', (e) => {
    call('/api/autostart', { enabled: e.target.checked })
      .then(() => say(e.target.checked ? 'VPN будет включаться вместе с телефоном' : 'Автозапуск выключен', 'ok'))
      .catch((error) => say(error.message, 'err'));
  });
  autoType.addEventListener('click', (event) => {
    const chip = event.target.closest('[data-auto]');
    if (!chip) return;
    for (const c of autoType.querySelectorAll('[data-auto]')) c.classList.toggle('is-on', c === chip);
    saveExtra({ autoConnectType: chip.dataset.auto });
  });
  pingView.addEventListener('click', (event) => {
    const chip = event.target.closest('[data-view]');
    if (!chip) return;
    for (const c of pingView.querySelectorAll('[data-view]')) c.classList.toggle('is-on', c === chip);
    try { localStorage.setItem('m-ping-display', chip.dataset.view); } catch (_) { /* не запомним */ }
    if (window.MS7RerenderSubs) window.MS7RerenderSubs();
  });
  $('m-mtu').addEventListener('change', (e) => {
    const value = parseInt(e.target.value, 10);
    if (e.target.value.trim() && (!(value >= 1280 && value <= 9000))) { say('MTU — число от 1280 до 9000', 'err'); return; }
    saveExtra({ tunMtu: e.target.value.trim() ? value : 0 });
  });

  // Остальное сохраняется само, без кнопки «Сохранить».
  const save = $('settings-save');
  if (save) {
    save.hidden = true;
    let timer = null;
    const later = (delay) => { clearTimeout(timer); timer = setTimeout(() => save.click(), delay); };
    const mine = (t) => t && t.closest && t.closest('#m-perapp, .m-toggle, #m-mtu, #m-strategy, #m-autotype, #m-pingview');
    page.addEventListener('change', (event) => {
      const t = event.target;
      if (mine(t)) return;
      if (t && (t.matches('input[type="checkbox"]') || t.tagName === 'SELECT')) later(250);
    });
    page.addEventListener('focusout', (event) => {
      const t = event.target;
      if (t && t.matches('input[type="text"]') && !mine(t)) later(250);
    });
    page.addEventListener('click', (event) => {
      if (event.target.closest('[data-theme-value], [data-dns]')) later(400);
    });
  }
})();
