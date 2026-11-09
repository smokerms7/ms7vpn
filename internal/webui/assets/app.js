/* MS7VPN — интерфейс окна. Вся логика остаётся в Go, страница только рисует
   состояние и дёргает локальный API. */
(() => {
  'use strict';

  // Токен берём только из адреса окна. В саму страницу он больше не
  // подставляется: раньше любой процесс мог запросить корень и вынуть его
  // прямо из HTML.
  const TOKEN = new URLSearchParams(location.search).get('token') || '';
  const AUTO_CONNECT = new URLSearchParams(location.search).get('autoconnect') || '';
  const BOT_URL = 'https://t.me/ms7vpn_bot';
  const flags = window.MS7Flags;
  let device = null;

  // ==== оформление ====
  // Настоящее значение приходит с состоянием из Go. Но состояние запрашивается
  // уже после отрисовки страницы, поэтому выбранную тему дополнительно
  // запоминаем в профиле окна и ставим сразу — иначе светлая тема успевала
  // мигнуть тёмной.
  const THEMES = ['brand', 'light', 'dark'];
  let theme = 'brand';
  // Пока человек не сохранил выбор, фоновый опрос состояния не должен
  // возвращать прежнюю тему: иначе выбранный цвет откатывался бы через
  // пару секунд или при переходе на другую страницу.
  let themeTouched = false;

  function applyTheme(name) {
    const next = THEMES.includes(name) ? name : 'brand';
    if (next === theme && document.documentElement.dataset.themeApplied === next) return;
    theme = next;
    document.documentElement.dataset.themeApplied = theme;
    // Фирменная тема — оформление по умолчанию, у неё атрибута нет.
    if (theme === 'brand') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', theme);
    // Обращаемся к DOM напрямую: помощник $ объявлен ниже, а эта функция
    // вызывается ещё до него, при первом применении запомненной темы.
    const group = document.getElementById('set-theme');
    if (group) {
      for (const button of group.querySelectorAll('[data-theme-value]')) {
        button.setAttribute('aria-checked', String(button.dataset.themeValue === theme));
      }
    }
    try {
      localStorage.setItem('ms7-theme', theme);
    } catch (_) {
      // Профиль окна может быть недоступен — тема просто не запомнится.
    }
  }

  try {
    const remembered = localStorage.getItem('ms7-theme');
    if (remembered) applyTheme(remembered);
  } catch (_) {
    // Ничего не делаем: останется фирменная тема.
  }

  let state = null;
  let busy = false;
  let timerHandle = null;
  let autoConnectDone = false;
  let search = '';
  // Номер поколения состояния. Фоновый опрос идёт каждые 2,5 с и раньше мог
  // затереть свежий ответ действия пользователя устаревшим снимком.
  let stateGeneration = 0;

  function applyState(next, generation) {
    if (generation !== undefined && generation !== stateGeneration) return false;
    state = next;
    return true;
  }

  function nodeCount(snapshot) {
    if (!snapshot) return 0;
    return (snapshot.subscriptions || []).reduce((sum, sub) => sum + ((sub.nodes || []).length), 0);
  }

  function nodeIdSet(snapshot) {
    const ids = new Set();
    for (const sub of (snapshot && snapshot.subscriptions) || []) {
      for (const node of sub.nodes || []) ids.add(node.id);
    }
    return ids;
  }

  // Человекочитаемый итог обновления подписки: сколько серверов стало и что
  // изменилось. Раньше кнопка «Обновить» молчала, и понять, сработала она
  // или нет, было невозможно.
  function describeRefresh(before, after) {
    const wasIds = nodeIdSet(before);
    const nowIds = nodeIdSet(after);
    let added = 0;
    for (const id of nowIds) if (!wasIds.has(id)) added++;
    let removed = 0;
    for (const id of wasIds) if (!nowIds.has(id)) removed++;

    const total = nodeCount(after);
    const parts = [`${total} ${plural(total, 'сервер', 'сервера', 'серверов')}`];
    if (added) parts.push(`+${added} ${plural(added, 'новый', 'новых', 'новых')}`);
    if (removed) parts.push(`−${removed} ${plural(removed, 'убран', 'убрано', 'убрано')}`);
    if (!added && !removed) parts.push('без изменений');
    return 'Обновлено: ' + parts.join(', ');
  }

  function plural(count, one, few, many) {
    const mod100 = count % 100;
    const mod10 = count % 10;
    if (mod100 >= 11 && mod100 <= 14) return many;
    if (mod10 === 1) return one;
    if (mod10 >= 2 && mod10 <= 4) return few;
    return many;
  }

  const $ = (id) => document.getElementById(id);

  async function api(path, options = {}) {
    const response = await fetch(path, {
      method: options.method || 'GET',
      headers: Object.assign({ 'X-MS7-Token': TOKEN }, options.body ? { 'Content-Type': 'application/json' } : {}),
      body: options.body ? JSON.stringify(options.body) : undefined
    });
    let payload = null;
    try { payload = await response.json(); } catch (_) { payload = null; }
    if (!response.ok || !payload || payload.ok !== true) {
      const message = (payload && payload.error && payload.error.message) || 'Не удалось выполнить запрос';
      const error = new Error(message);
      error.code = payload && payload.error ? payload.error.code : 'ERROR';
      throw error;
    }
    return payload.data;
  }

  // История сообщений: всплывашка живёт 2 секунды, но само сообщение
  // не пропадает бесследно — его можно перечитать на странице «Журнал».
  const events = [];

  function toast(text, kind) {
    const node = $('toast');
    node.textContent = text;
    node.className = 'toast' + (kind ? ' is-' + kind : '');
    node.hidden = false;
    clearTimeout(node._timer);
    node._timer = setTimeout(() => { node.hidden = true; }, 2000);

    events.unshift({ text, kind, at: new Date() });
    if (events.length > 30) events.pop();
    renderEvents();
  }

  function renderEvents() {
    const list = $('events-list');
    if (!list) return;
    if (!events.length) {
      list.innerHTML = '<span class="events-empty">Пока ничего не происходило.</span>';
      return;
    }
    list.innerHTML = events.map((item) => `
      <div class="event${item.kind ? ' is-' + item.kind : ''}">
        <span class="event-time">${item.at.toLocaleTimeString('ru-RU')}</span>
        <span class="event-text">${escapeHtml(item.text)}</span>
      </div>`).join('');
  }

  const escapeHtml = (value) => String(value == null ? '' : value)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

  function allNodes() {
    if (!state) return [];
    const list = [];
    for (const sub of state.subscriptions || []) {
      for (const node of sub.nodes || []) list.push(Object.assign({ subId: sub.id, subName: sub.name }, node));
    }
    return list;
  }

  // Имя сервера: своё, если человек переименовал, иначе из подписки.
  function nodeName(node) {
    const custom = state && state.nodeNames && state.nodeNames[node.id];
    return custom || flags.cleanName(node.name);
  }

  // Активная подписка: выбранная человеком, иначе подписка выбранного
  // сервера, иначе первая.
  function activeSubId() {
    const subs = (state && state.subscriptions) || [];
    if (!subs.length) return '';
    if (subs.some((s) => s.id === state.selectedSubscriptionId)) return state.selectedSubscriptionId;
    const node = allNodes().find((n) => n.id === state.selectedNodeId);
    return node ? node.subId : subs[0].id;
  }

  function findNode(id) {
    return allNodes().find((node) => node.id === id) || null;
  }

  function pingClass(node) {
    if (!node.pingMs || node.pingStatus !== 'ok') return '';
    if (node.pingMs <= 80) return 'is-fast';
    if (node.pingMs <= 200) return 'is-mid';
    return 'is-bad';
  }

  function nodeMeta(node) {
    const parts = [node.protocol, node.transport, node.security]
      .map((value) => String(value || '').trim())
      .filter((value) => value && value.toLowerCase() !== 'none');
    return parts.map((value) => value.toUpperCase()).join(' / ');
  }

  function pingText(node) {
    if (node.pingStatus === 'error') return 'нет связи';
    if (node.pingStatus === 'ok' && node.pingMs) return node.pingMs + ' мс';
    return '—';
  }

  function formatUptime(startedAt) {
    const started = Date.parse(startedAt || '');
    if (!started || Number.isNaN(started)) return '00:00:00';
    let seconds = Math.max(0, Math.floor((Date.now() - started) / 1000));
    const h = String(Math.floor(seconds / 3600)).padStart(2, '0');
    const m = String(Math.floor((seconds % 3600) / 60)).padStart(2, '0');
    const s = String(seconds % 60).padStart(2, '0');
    return `${h}:${m}:${s}`;
  }

  function formatBytes(value) {
    const bytes = Number(value) || 0;
    if (bytes <= 0) return '0 ГБ';
    const gb = bytes / (1024 * 1024 * 1024);
    if (gb >= 1) return gb.toFixed(1).replace('.', ',') + ' ГБ';
    return (bytes / (1024 * 1024)).toFixed(0) + ' МБ';
  }

  // Коротко для главной: сегодня — только время, иначе день и месяц.
  function shortDate(value) {
    const time = Date.parse(value || '');
    if (!time || Number.isNaN(time)) return '—';
    const date = new Date(time);
    if (date.toDateString() === new Date().toDateString()) return 'в ' + date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });
    return date.toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' });
  }

  function formatDate(value) {
    const time = Date.parse(value || '');
    if (!time || Number.isNaN(time)) return '—';
    return new Date(time).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' });
  }

  /* ==================== отрисовка ==================== */

  function render() {
    if (!state) return;
    const connection = state.connection || {};
    const connected = !!connection.connected;
    const connecting = !!connection.connecting;

    $('foot-version').textContent = state.version || '';

    const pill = $('status-pill');
    pill.className = 'status-pill' + (connected ? ' is-on' : connecting ? ' is-wait' : '');
    $('status-text').textContent = connected ? 'Подключено' : connecting ? 'Подключение…' : 'Отключено';

    const power = $('power-btn');
    power.setAttribute('aria-label', connected ? 'Отключить' : connecting ? 'Отменить подключение' : 'Подключить');
    // Кнопка не гасится во время подключения: нажатие отменяет его.
    setText($('power-label'), connected ? 'Отключить' : connecting ? 'Подключение…' : fastestRunning ? 'Поиск…' : 'Подключить');
    setText($('power-state'), connected ? 'Подключено' : connecting ? 'Подключение' : 'Отключено');
    const stage = $('power-stage');
    stage.classList.toggle('is-on', connected);
    stage.classList.toggle('is-wait', connecting || fastestRunning);
    $('shell').classList.toggle('is-on', connected);
    // Короткая вспышка кольца в момент подключения.
    if (connected && !wasConnected) {
      const burst = $('power-burst');
      burst.classList.remove('go');
      void burst.offsetWidth;
      burst.classList.add('go');
      speedHistory.length = 0;
    }
    wasConnected = connected;

    const selected = findNode(state.selectedNodeId);
    setText($('chip-name'), selected ? nodeName(selected) : ((state.subscriptions || []).length ? 'Выберите сервер' : 'Добавьте подписку'));
    const chipFlagName = selected ? selected.name : '';
    if ($('chip-flag').dataset.name !== chipFlagName) {
      $('chip-flag').dataset.name = chipFlagName;
      $('chip-flag').innerHTML = selected ? flags.flagHTML(selected.name) : '';
    }
    const chipPing = $('chip-ping');
    const livePing = connected ? connection.pingMs : 0;
    const shownPing = livePing > 0 ? livePing : (selected && selected.pingStatus === 'ok' ? selected.pingMs : 0);
    chipPing.hidden = !shownPing;
    if (shownPing) {
      setText(chipPing, shownPing + ' мс');
      chipPing.className = 'chip-ping ' + pingClass({ pingMs: shownPing, pingStatus: 'ok' });
    }

    renderSpeed(connection, connected);
    renderGreeting(connected, connecting, selected);
    renderQuick();


    const problem = connection.lastError || (state.core && state.core.lastError) || '';
    // Строка под кнопкой, как у Shade: что происходит прямо сейчас.
    const subtxt = $('home-subtxt');
    let line;
    // Подключено — строка не нужна: сервер виден на плашке ниже, статус на кнопке.
    if (connected) line = isZapret() ? 'Обход блокировок работает — трафик идёт напрямую' : '';
    else if (connecting) line = connection.statusMessage || 'Подключаемся…';
    else if (problem) line = problem;
    else if (isZapret()) line = 'Обход DPI без сервера: пинг родной, подписка не тратится';
    else if (!(state.subscriptions || []).length) line = 'Добавьте подписку — Ctrl+V с ссылкой в буфере';
    else line = 'Нажмите, чтобы подключиться';
    setText(subtxt, line);
    subtxt.hidden = !line;
    subtxt.className = 'subtxt' + (connected ? ' is-on' : !connecting && problem ? ' is-err' : '');

    const notice = $('home-notice');
    const warning = (connection.warnings || []).join('\n');
    notice.className = 'notice';
    if (warning) {
      notice.hidden = false;
      notice.className = 'notice is-warn';
      notice.textContent = warning;
    } else {
      notice.hidden = true;
    }

    renderServers();
    renderHomeSubscription();
    // После карточки подписки: в «Запрете» она не нужна и гасится здесь.
    renderZapret();
    renderSubs();
    renderSettings();
    renderSplit(false);
    renderInfo();
    updateTimer();
  }

  // Отрисовка списка серверов «по месту».
  //
  // Раньше список целиком пересобирался через innerHTML каждые 2,5 секунды:
  // сбрасывалась прокрутка, пропадало наведение, а на подписке в несколько
  // сотен серверов окно заметно дёргалось. Теперь узлы переиспользуются,
  // а меняются только те значения, которые действительно изменились.
  const rowCache = new Map();

  function buildRow(node) {
    const row = document.createElement('div');
    row.className = 'row-item';
    row.dataset.node = node.id;
    row.innerHTML = `
      <span class="flag-slot"></span>
      <span class="row-main">
        <span class="row-title"></span>
        <span class="row-sub"></span>
      </span>
      <span class="ping"></span>
      <button type="button" class="srv-info-btn" data-info="${escapeHtml(node.id)}" aria-label="О сервере" title="О сервере и переименование">
        <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/></svg>
      </button>
      <button type="button" class="star" data-fav="${escapeHtml(node.id)}" aria-label="В избранное">
        <svg viewBox="0 0 24 24"><path d="M12 3.6l2.6 5.3 5.8.8-4.2 4.1 1 5.8-5.2-2.7-5.2 2.7 1-5.8L3.6 9.7l5.8-.8z"/></svg>
      </button>`;
    return row;
  }

  function setText(element, value) {
    if (element.textContent !== value) element.textContent = value;
  }

  function updateRow(row, node, favorites) {
    const flagSlot = row.querySelector('.flag-slot');
    const flagHTML = flags.flagHTML(node.name);
    if (flagSlot.dataset.name !== node.name) {
      flagSlot.dataset.name = node.name;
      flagSlot.innerHTML = flagHTML;
    }
    setText(row.querySelector('.row-title'), nodeName(node));
    setText(row.querySelector('.row-sub'), node.supported ? nodeMeta(node) : (node.unsupportedReason || 'Не поддерживается'));

    const ping = row.querySelector('.ping');
    setText(ping, pingText(node));
    const pingClassName = 'ping ' + pingClass(node);
    if (ping.className !== pingClassName) ping.className = pingClassName;

    row.classList.toggle('is-selected', node.id === state.selectedNodeId);
    row.classList.toggle('is-off', !node.supported);
    row.querySelector('.star').classList.toggle('is-on', !!favorites[node.id]);
  }

  function renderServers() {
    const list = $('server-list');
    const favorites = state.favorites || {};
    const needle = search.trim().toLowerCase();

    const order = (state.settings || {}).serverSort || 'ping';
    const activeId = activeSubId();
    const visible = allNodes()
      .filter((node) => !activeId || node.subId === activeId)
      .map((node, index) => Object.assign(node, { order: index }))
      .filter((node) => !needle || (node.name || '').toLowerCase().includes(needle));
    visible.sort((a, b) => {
      const favDiff = (favorites[b.id] ? 1 : 0) - (favorites[a.id] ? 1 : 0);
      if (favDiff) return favDiff;
      if (order === 'none') return a.order - b.order;
      if (order === 'name') return nodeName(a).localeCompare(nodeName(b), 'ru');
      const aPing = a.pingStatus === 'ok' ? a.pingMs : 99999;
      const bPing = b.pingStatus === 'ok' ? b.pingMs : 99999;
      if (aPing !== bPing) return aPing - bPing;
      return a.order - b.order;
    });

    $('servers-empty').hidden = visible.length > 0;
    // Кнопку показываем только когда подписок нет вовсе. Если серверы просто
    // не нашлись поиском — добавлять нечего, предлагать нечего.
    $('servers-add-sub').hidden = (state.subscriptions || []).length > 0;

    const seen = new Set();
    let position = list.firstElementChild;
    for (const node of visible) {
      seen.add(node.id);
      let row = rowCache.get(node.id);
      if (!row) {
        row = buildRow(node);
        rowCache.set(node.id, row);
      }
      updateRow(row, node, favorites);
      if (position === row) {
        position = row.nextElementSibling;
      } else {
        list.insertBefore(row, position);
      }
    }
    while (position) {
      const next = position.nextElementSibling;
      list.removeChild(position);
      position = next;
    }
    for (const [id, row] of rowCache) {
      if (!seen.has(id) && !row.isConnected) rowCache.delete(id);
    }
  }

  // Скорость приходит из счётчиков Xray в битах в секунду.
  function formatSpeed(bitsPerSecond) {
    const bits = Math.max(0, Number(bitsPerSecond) || 0);
    const mbit = bits / 1e6;
    if (mbit >= 1000) return (mbit / 1000).toFixed(1).replace('.', ',') + '<small>Гбит/с</small>';
    if (mbit >= 1) return mbit.toFixed(1).replace('.', ',') + '<small>Мбит/с</small>';
    const kbit = bits / 1e3;
    if (kbit >= 1) return kbit.toFixed(0) + '<small>Кбит/с</small>';
    return '0,0<small>Мбит/с</small>';
  }

  function renderSpeed(connection, connected) {
    const down = $('speed-down');
    const up = $('speed-up');
    const downHTML = compactSpeed(connected ? connection.downlinkBitsSec : 0);
    const upHTML = compactSpeed(connected ? connection.uplinkBitsSec : 0);
    if (down.innerHTML !== downHTML) down.innerHTML = downHTML;
    if (up.innerHTML !== upHTML) up.innerHTML = upHTML;
    $('speed-row').classList.toggle('is-idle', !connected);

    // Пинг через туннель: меряется программой раз в несколько секунд.
    const ping = $('stat-ping');
    const ms = connected ? Number(connection.pingMs) || 0 : 0;
    const pingHTML = ms > 0 ? ms + '<small>мс</small>' : ms < 0 ? '×' : '—';
    if (ping.innerHTML !== pingHTML) ping.innerHTML = pingHTML;
    ping.classList.toggle('is-bad', ms < 0 || ms > 300);

  }

  // Скорость для узкой плитки под кнопкой: число и короткая единица.
  function compactSpeed(bitsPerSecond) {
    const bits = Math.max(0, Number(bitsPerSecond) || 0);
    const mbit = bits / 1e6;
    if (mbit >= 100) return Math.round(mbit) + '<small>Мб/с</small>';
    if (mbit >= 1) return mbit.toFixed(1).replace('.', ',') + '<small>Мб/с</small>';
    if (bits >= 1e3) return Math.round(bits / 1e3) + '<small>Кб/с</small>';
    return '0,0<small>Мб/с</small>';
  }

  // Длительность сессии как у Shade: 05:12 или 1:05:12.
  function shortUptime(startedAt) {
    const started = Date.parse(startedAt || '');
    if (!started || Number.isNaN(started)) return '00:00';
    const total = Math.max(0, Math.floor((Date.now() - started) / 1000));
    const pad = (n) => String(n).padStart(2, '0');
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = total % 60;
    return h > 0 ? h + ':' + pad(m) + ':' + pad(s) : pad(m) + ':' + pad(s);
  }

  function formatTraffic(value) {
    const bytes = Number(value) || 0;
    if (bytes >= 1024 ** 3) return (bytes / 1024 ** 3).toFixed(2).replace('.', ',') + ' ГБ';
    if (bytes >= 1024 ** 2) return (bytes / 1024 ** 2).toFixed(bytes >= 100 * 1024 ** 2 ? 0 : 1).replace('.', ',') + ' МБ';
    if (bytes > 0) return Math.max(1, Math.round(bytes / 1024)) + ' КБ';
    return '0 МБ';
  }

  /* ==== график скорости ====
     Точки берутся из того же опроса состояния. Холст перерисовывается
     только когда пришла новая точка или изменился размер. */
  const speedHistory = [];
  const HISTORY_POINTS = 90;
  let wasConnected = false;
  let fastestRunning = false;

  function pushSpeedSample() {
    const connection = (state && state.connection) || {};
    if (!connection.connected) return;
    speedHistory.push({ down: Number(connection.downlinkBitsSec) || 0, up: Number(connection.uplinkBitsSec) || 0 });
    if (speedHistory.length > HISTORY_POINTS) speedHistory.shift();
    drawChart();
  }

  function cssVar(name, fallback) {
    const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    return value || fallback;
  }

  function drawChart() {
    const canvas = $('speed-chart');
    if (!canvas || !canvas.offsetParent) return;
    const ratio = window.devicePixelRatio || 1;
    const width = canvas.clientWidth;
    const height = canvas.clientHeight;
    if (!width || !height) return;
    if (canvas.width !== Math.round(width * ratio) || canvas.height !== Math.round(height * ratio)) {
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
    }
    const ctx = canvas.getContext('2d');
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, width, height);

    // Сетка — три тонкие линии, чтобы было на что опереться глазу.
    ctx.strokeStyle = cssVar('--track', 'rgba(255,255,255,0.07)');
    ctx.lineWidth = 1;
    for (let i = 1; i <= 3; i++) {
      const y = Math.round((height / 4) * i) + 0.5;
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(width, y); ctx.stroke();
    }

    const points = speedHistory;
    if (points.length < 2) {
      ctx.fillStyle = cssVar('--muted', '#9A90AD');
      ctx.font = '12px "Segoe UI", sans-serif';
      ctx.textAlign = 'center';
      ctx.fillText(state && state.connection && state.connection.connected ? 'Собираем данные…' : 'График появится после подключения', width / 2, height / 2 + 4);
      return;
    }
    const max = Math.max(1e6, ...points.map((p) => Math.max(p.down, p.up))) * 1.15;
    const step = width / (HISTORY_POINTS - 1);
    const offset = (HISTORY_POINTS - points.length) * step;
    const plot = (key, color, fill) => {
      ctx.beginPath();
      points.forEach((p, i) => {
        const x = offset + i * step;
        const y = height - (p[key] / max) * (height - 4) - 2;
        if (i === 0) ctx.moveTo(x, y);
        else {
          const px = offset + (i - 1) * step;
          const py = height - (points[i - 1][key] / max) * (height - 4) - 2;
          const cx = (px + x) / 2;
          ctx.bezierCurveTo(cx, py, cx, y, x, y);
        }
      });
      ctx.strokeStyle = color;
      ctx.lineWidth = 2;
      ctx.lineJoin = 'round';
      ctx.stroke();
      if (fill) {
        ctx.lineTo(offset + (points.length - 1) * step, height);
        ctx.lineTo(offset, height);
        ctx.closePath();
        const gradient = ctx.createLinearGradient(0, 0, 0, height);
        gradient.addColorStop(0, color + '55');
        gradient.addColorStop(1, color + '00');
        ctx.fillStyle = gradient;
        ctx.fill();
      }
    };
    plot('up', cssVar('--chart-up', '#B026FF'), false);
    plot('down', cssVar('--chart-down', '#35D07F'), true);

    // Подпись максимума в углу.
    ctx.fillStyle = cssVar('--muted', '#9A90AD');
    ctx.font = '10px "Segoe UI", sans-serif';
    ctx.textAlign = 'right';
    ctx.fillText(formatSpeed(max / 1.15).replace(/<small>(.*)<\/small>/, ' $1'), width - 2, 10);
  }
  window.addEventListener('resize', () => drawChart());

  /* ==== приветствие ==== */
  function renderGreeting(connected, connecting, selected) {
    const hour = new Date().getHours();
    const title = hour < 5 ? 'Доброй ночи' : hour < 12 ? 'Доброе утро' : hour < 18 ? 'Добрый день' : 'Добрый вечер';
    setText($('greet-title'), title + ' 👋');
    let sub;
    if (isZapret()) {
      sub = connected ? 'Обход блокировок работает — трафик идёт напрямую' : 'Режим «Запрет»: обход без сервера';
    } else if (connected) {
      const where = selected ? nodeName(selected) : 'сервер';
      sub = 'Соединение защищено · ' + where;
    } else if (connecting) {
      sub = 'Подключаемся…';
    } else if (!(state.subscriptions || []).length) {
      sub = 'Добавьте подписку — нажмите Ctrl+V с ссылкой в буфере';
    } else {
      sub = 'Нажмите кнопку, чтобы подключиться';
    }
    setText($('greet-sub'), sub);
  }

  /* ==== быстрые действия и «Без VPN» на главной ==== */
  function renderQuick() {
    const settings = state.settings || {};
    const on = !!settings.directRu || (settings.directDomains || []).length > 0 || (settings.directApps || []).length > 0;
    $('nav-split-badge').hidden = !on;
  }

  // Статус подписки. Возвращает null, если панель не прислала ничего,
  // из чего его можно было бы честно вывести.
  function subscriptionStatus(info) {
    const raw = String(info.status || '').toUpperCase();
    if (raw === 'ACTIVE') return { text: 'Активна', kind: '' };
    if (raw === 'EXPIRED') return { text: 'Истекла', kind: 'danger' };
    if (raw === 'LIMITED') return { text: 'Лимит исчерпан', kind: 'danger' };
    if (raw === 'DISABLED') return { text: 'Отключена', kind: 'danger' };
    if (raw) return { text: raw, kind: '' };

    // Статуса нет — выводим его из срока и лимита, если панель их прислала.
    const used = (info.uploadBytes || 0) + (info.downloadBytes || 0);
    if (info.totalBytes > 0 && used >= info.totalBytes) return { text: 'Лимит исчерпан', kind: 'danger' };
    if (info.expireUnix) {
      const days = Math.floor((info.expireUnix * 1000 - Date.now()) / 86400000);
      if (days < 0) return { text: 'Истекла', kind: 'danger' };
      if (days <= 3) return { text: days === 0 ? 'Истекает сегодня' : `Осталось ${days} ${plural(days, 'день', 'дня', 'дней')}`, kind: 'warn' };
      return { text: 'Активна', kind: '' };
    }
    return null;
  }

  function renderHomeSubscription() {
    const card = $('home-sub-card');
    const subs = state.subscriptions || [];
    const selected = findNode(state.selectedNodeId);
    const current = subs.find((sub) => sub.id === activeSubId()) || subs[0];
    if (!current) {
      card.hidden = true;
      return;
    }
    card.hidden = false;
    card.dataset.sub = current.id;
    setText($('home-sub-name'), current.name || 'Подписка');

    const info = current.userInfo || {};
    const used = (info.uploadBytes || 0) + (info.downloadBytes || 0);
    const total = info.totalBytes || 0;

    // Ниже показывается ТОЛЬКО то, что действительно прислала панель.
    // Ничего не достраивается: нет срока — нет строки, нет лимита — нет полосы.
    const expireRow = $('home-sub-expire-row');
    const daysBox = $('home-sub-days');
    if (info.expireUnix) {
      expireRow.hidden = false;
      setText($('home-sub-expire'),
        new Date(info.expireUnix * 1000).toLocaleDateString('ru-RU', { day: 'numeric', month: 'short', year: 'numeric' }));
      // Кружок с днями: полный — месяц и больше, дальше убывает.
      const daysLeft = Math.max(0, Math.ceil((info.expireUnix * 1000 - Date.now()) / 86400000));
      daysBox.hidden = false;
      const daysHTML = (daysLeft > 999 ? '∞' : daysLeft) + '<small>' + plural(daysLeft, 'день', 'дня', 'дней') + '</small>';
      if ($('home-sub-days-num').innerHTML !== daysHTML) $('home-sub-days-num').innerHTML = daysHTML;
      const share = Math.min(1, daysLeft / 30);
      $('home-sub-days-fill').style.strokeDashoffset = String(97.4 * (1 - share));
      daysBox.className = 'sub-days' + (daysLeft <= 3 ? ' is-danger' : daysLeft <= 7 ? ' is-warn' : '');
    } else {
      expireRow.hidden = true;
      daysBox.hidden = true;
    }

    const badge = $('home-sub-status');
    const statusText = subscriptionStatus(info);
    badge.hidden = !statusText;
    if (statusText) {
      setText(badge, statusText.text);
      const cls = 'badge' + (statusText.kind ? ' is-' + statusText.kind : '');
      if (badge.className !== cls) badge.className = cls;
    }

    // Трафик показывается всегда: при безлимите полоса пустая, а вместо
    // лимита стоит плашка «Безлимит» — так это выглядит у других клиентов.
    const limitBadge = $('home-sub-limit');
    const bar = $('home-sub-bar');
    const fill = bar.firstElementChild;
    const hasLimit = total > 0;

    setText($('home-sub-used'), formatBytes(used));
    if (hasLimit) {
      const percent = Math.min(100, Math.round((used / total) * 100));
      setText(limitBadge, percent + '%');
      limitBadge.className = 'badge is-quiet' + (percent >= 90 ? ' is-danger' : percent >= 75 ? ' is-warn' : '');
      fill.style.width = percent + '%';
      fill.className = percent >= 90 ? 'is-danger' : percent >= 75 ? 'is-warn' : '';
      setText($('home-sub-left'), 'из ' + formatBytes(total));
    } else {
      setText(limitBadge, '∞ Безлимит');
      limitBadge.className = 'badge is-quiet';
      fill.style.width = '0%';
      fill.className = '';
      setText($('home-sub-left'), 'без ограничений');
    }

    const warn = $('home-sub-warn');
    warn.hidden = !current.lastError;
    if (current.lastError) setText(warn, '⚠ ' + current.lastError);
    if (/лимит устройств/i.test(current.lastError || '')) {
      setText(badge, 'Лимит устройств');
      badge.hidden = false;
      badge.className = 'badge is-warn';
    }

    const count = (current.nodes || []).length;
    setText($('home-sub-hint'),
      `${count} ${plural(count, 'сервер', 'сервера', 'серверов')} · обновлено ${shortDate(current.updatedAt)}`);
  }

  function renderSubs() {
    const list = $('sub-list');
    const subs = state.subscriptions || [];
    $('subs-empty').hidden = subs.length > 0;
    const activeId = activeSubId();
    list.innerHTML = subs.map((sub) => {
      const info = sub.userInfo || {};
      const used = (info.uploadBytes || 0) + (info.downloadBytes || 0);
      const traffic = info.totalBytes ? `${formatBytes(used)} из ${formatBytes(info.totalBytes)}` : formatBytes(used) + ' использовано';
      const expire = info.expireUnix ? ' · до ' + new Date(info.expireUnix * 1000).toLocaleDateString('ru-RU') : '';
      return `
      <div class="row-item sub-row-item${sub.id === activeId ? ' is-selected' : ''}" data-sub="${escapeHtml(sub.id)}" data-sub-select="${escapeHtml(sub.id)}" title="${sub.id === activeId ? 'Активная подписка' : 'Нажмите, чтобы сделать активной'}">
        <span class="sub-radio${sub.id === activeId ? ' is-on' : ''}"></span>
        <span class="row-main">
          <span class="row-title">${escapeHtml(sub.name || 'Подписка')}${sub.id === activeId ? '<span class="sub-active-tag">активна</span>' : ''}</span>
          <span class="row-sub">${(sub.nodes || []).length} серверов · обновлено ${escapeHtml(formatDate(sub.updatedAt))}</span>
          <span class="row-sub">${escapeHtml(traffic + expire)}</span>
          ${sub.lastError ? `<span class="row-sub" style="color:#FFB3BE">${escapeHtml(sub.lastError)}</span>` : ''}
        </span>
        <button type="button" class="btn btn-ghost" data-sub-refresh="${escapeHtml(sub.id)}">Обновить</button>
        <button type="button" class="btn btn-danger" data-sub-delete="${escapeHtml(sub.id)}">Удалить</button>
      </div>`;
    }).join('');
  }

  function renderSettings() {
    const settings = state.settings || {};
    if (document.activeElement && document.activeElement.closest && document.activeElement.closest('#page-settings')) return;
    $('set-mode').value = settings.connectionMode || 'tun';
    $('set-engine').value = settings.engine || 'xray';
    $('set-autorefresh').checked = !!settings.autoRefresh;
    $('set-autoconnect').checked = !!settings.autoConnect;
    $('set-blockads').checked = !!settings.blockAds;
    $('set-tray').checked = settings.minimizeToTray !== false;
    $('set-dns1').value = settings.dns1 || '';
    $('set-dns2').value = settings.dns2 || '';
    $('set-testurl').value = settings.testUrl || '';
    $('set-pingmode').value = settings.pingMode || 'real-get';
    $('set-pingtimeout').value = String(settings.pingTimeoutMs || 5000);
    $('set-consoleproxy').checked = !!settings.consoleProxy;
    $('set-autoping').checked = !!settings.autoPing;
    $('set-sort').value = settings.serverSort || 'ping';
    renderDnsChips();
    if (!themeTouched) applyTheme(settings.theme);
    renderZapretSettings();
  }

  function renderInfo() {
    const core = state.core || {};
    if ($('update-title').textContent === 'Проверка обновлений') {
      setText($('update-sub'), 'Установлена версия ' + (state.version || '—'));
    }
    $('info-version').textContent = state.version || '—';
    $('info-core').textContent = `${core.engine === 'sing-box' ? 'sing-box' : 'Xray'} ${core.version || ''}`;
    $('info-core-state').textContent = core.downloading
      ? `Загрузка ядра… ${core.progress || 0}%`
      : core.installed ? 'Выбранное ядро установлено' : 'Ядро загрузится при первом подключении';
    $('info-device').textContent = (device && device.deviceName) || '—';
    $('info-os').textContent = (device && device.osName) || 'Windows';
    $('info-id').textContent = (device && device.deviceId) || '—';
  }

  function updateTimer() {
    const connection = (state && state.connection) || {};
    const uptime = connection.connected ? formatUptime(connection.startedAt) : '00:00:00';
    $('power-timer').textContent = uptime;
    setText($('zapret-session'), uptime);
  }

  // Приложение открывается сразу, даже когда подписок нет.
  //
  // Раньше здесь был отдельный экран «добавьте подписку», который прятал
  // всё окно целиком. Пока VPN был единственным режимом, это было разумно.
  // С появлением «Запрета» стало мешать: он подписки не требует, а человек,
  // пришедший именно за ним, упирался в требование, которое к нему не
  // относится. Ссылку можно добавить позже на вкладке «Подписки» — там есть
  // и поле, и кнопки бота.

  async function openExternal(url) {
    try {
      await api('/api/open', { method: 'POST', body: { url } });
    } catch (error) {
      toast(error.message, 'err');
    }
  }

  async function addSubscription(url) {
    if (!url) { toast('Вставьте ссылку подписки', 'err'); return; }
    await guard(async () => {
      state = await api('/api/subscriptions/add', { method: 'POST', body: { url, name: '' } });
      $('sub-url').value = '';
      toast('Подписка добавлена', 'ok');
    });
  }

  /* ==================== действия ==================== */

  // Фоновый опрос состояния. Во время действия пользователя он не трогает
  // state: иначе ответ «подписка обновлена» мог быть затёрт снимком, который
  // ушёл на сервер раньше.
  async function refresh() {
    if (busy) return;
    const generation = stateGeneration;
    try {
      const next = await api('/api/state');
      if (busy || !applyState(next, generation)) return;
      render();
      followPick();
      if (AUTO_CONNECT && !autoConnectDone && !state.connection.connected && !state.connection.connecting) {
        autoConnectDone = true;
        connect(AUTO_CONNECT);
      }
    } catch (error) {
      toast(error.message, 'err');
    }
  }

  async function guard(action) {
    if (busy) return;
    busy = true;
    stateGeneration++;
    try {
      await action();
    } catch (error) {
      if (error.code === 'NEED_ADMIN') {
        await elevate();
      } else {
        toast(error.message, 'err');
      }
    } finally {
      busy = false;
      render();
    }
  }

  // Общая процедура обновления подписок с внятным результатом.
  async function refreshSubscriptions(ids, button) {
    const before = state;
    const label = button ? button.textContent : '';
    if (button) {
      button.disabled = true;
      button.textContent = 'Обновляем…';
    }
    try {
      let latest = state;
      for (const id of ids) {
        latest = await api('/api/subscriptions/refresh', { method: 'POST', body: { id } });
      }
      state = latest;
      toast(describeRefresh(before, latest), 'ok');
    } finally {
      if (button) {
        button.disabled = false;
        button.textContent = label;
      }
    }
  }

  async function elevate() {
    try {
      await api('/api/elevate', { method: 'POST', body: { nodeId: state.selectedNodeId || '' } });
      toast('Перезапуск с правами администратора…', 'ok');
    } catch (error) {
      toast(error.message, 'err');
    }
  }

  // Анимация подключения: кнопка сразу переходит в «Подключение», дуга
  // крутится, а результат показывается не раньше чем через MIN_CONNECT_MS.
  // Раньше окно не перерисовывалось, пока шёл запрос, и быстрое подключение
  // выглядело как мгновенный щелчок без всякой анимации.
  const MIN_CONNECT_MS = 1400;
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  function showPending(message) {
    state.connection = Object.assign({}, state.connection, {
      connecting: true, connected: false, lastError: '', statusMessage: message
    });
    render();
  }

  // Выбор подписки нажатием. Если VPN включён на сервере другой подписки —
  // переподключаемся на сервер выбранной.
  async function selectSubscription(id) {
    if (!id || id === activeSubId()) return;
    const wasConnected = !!(state.connection || {}).connected;
    const before = state.selectedNodeId;
    await guard(async () => {
      state = await api('/api/subscriptions/select', { method: 'POST', body: { id } });
      const sub = (state.subscriptions || []).find((s) => s.id === id);
      toast('Активная подписка: ' + ((sub && sub.name) || 'Подписка'), 'ok');
    });
    if (wasConnected && state.selectedNodeId && state.selectedNodeId !== before) connect(state.selectedNodeId);
  }

  async function connect(nodeId) {
    if (!state) return;
    await guard(async () => {
      const started = Date.now();
      showPending(isZapret() ? 'Включаем обход…' : 'Подключаемся…');
      let result;
      let failure;
      try {
        result = await api('/api/connect', { method: 'POST', body: { nodeId: nodeId || state.selectedNodeId || '', mode: '' } });
      } catch (error) {
        failure = error;
      }
      await wait(Math.max(0, MIN_CONNECT_MS - (Date.now() - started)));
      if (failure) {
        // Состояние после ошибки — с сервера, иначе кнопка осталась бы
        // в «Подключение».
        try { state = await api('/api/state'); } catch (_) { /* покажем как есть */ }
        throw failure;
      }
      state = result;
    });
  }

  async function disconnect() {
    await guard(async () => {
      const started = Date.now();
      state.connection = Object.assign({}, state.connection, { statusMessage: 'Отключаемся…' });
      $('power-stage').classList.add('is-wait');
      setText($('power-label'), 'Отключение…');
      const result = await api('/api/disconnect', { method: 'POST', body: {} });
      await wait(Math.max(0, 600 - (Date.now() - started)));
      state = result;
    });
  }

  /* ==================== события ==================== */

  document.querySelectorAll('.nav-item').forEach((button) => {
    button.addEventListener('click', () => {
      document.querySelectorAll('.nav-item').forEach((item) => item.classList.toggle('is-active', item === button));
      const page = button.dataset.page;
      document.querySelectorAll('.page').forEach((section) => {
        section.classList.toggle('is-active', section.id === 'page-' + page);
      });
      if (page === 'log') loadLog();
      if (page === 'stats') loadUsage();
      if (page === 'split') renderSplit(true);
      if (page === 'home') requestAnimationFrame(drawChart);
      currentPage = page;
    });
  });

  const openPage = (page) => {
    const button = document.querySelector(`.nav-item[data-page="${page}"]`);
    if (button) button.click();
  };
  $('server-chip').addEventListener('click', () => openPage('servers'));

  // Удержание кнопки секунду — подключиться к самому быстрому серверу.
  // Кольцо начинает заполняться через 150 мс, чтобы обычный щелчок не
  // мигал дугой.
  const HOLD_DELAY = 150;
  const HOLD_MS = 1000;
  let holdTimers = [];
  let holdFired = false;

  function clearHold() {
    holdTimers.forEach((id) => clearTimeout(id));
    holdTimers = [];
    $('power-stage').classList.remove('is-holding');
  }

  function canHold() {
    return state && !isZapret() && !state.connection.connected && !state.connection.connecting && !fastestRunning && allNodes().some((n) => n.supported);
  }

  $('power-btn').addEventListener('pointerdown', (event) => {
    holdFired = false;
    if (event.button !== 0 || !canHold()) return;
    clearHold();
    holdTimers.push(
      setTimeout(() => $('power-stage').classList.add('is-holding'), HOLD_DELAY),
      setTimeout(() => {
        holdFired = true;
        clearHold();
        fastest(true);
      }, HOLD_MS)
    );
  });
  ['pointerup', 'pointerleave', 'pointercancel'].forEach((name) => $('power-btn').addEventListener(name, clearHold));

  $('power-btn').addEventListener('click', () => {
    if (holdFired) { holdFired = false; return; }
    togglePower();
  });

  // Нажатие во время подключения отменяет его. Идём мимо guard: тот занят
  // ожиданием самого подключения.
  async function cancelConnect() {
    try {
      state = await api('/api/disconnect', { method: 'POST', body: {} });
      toast('Подключение отменено');
    } catch (error) {
      toast(error.message, 'err');
    }
    render();
  }

  function togglePower() {
    if (!state) return;
    if (state.connection.connecting) { cancelConnect(); return; }
    if (state.connection.connected) { disconnect(); return; }
    // Режим VPN без единой подписки подключать не к чему. Раньше запрос
    // уходил на сервер и возвращался технической ошибкой.
    if (!isZapret() && (state.subscriptions || []).length === 0) {
      toast('Сначала добавьте подписку', 'err');
      openPage('subs');
      return;
    }
    // Первое включение «Запрета», когда стратегия ни разу не подбиралась:
    // включать наугад бессмысленно — у каждого провайдера работает своя.
    // Поэтому открываем подбор и включаемся сами, если он нашёл стратегию,
    // открывающую и YouTube, и Discord.
    if (isZapret() && !(state.zapret || {}).picked && (state.zapret || {}).available) {
      enableAfterPick = true;
      openPage('settings');
      openStrategyModal(true);
      return;
    }
    // Сервер не выбран — сразу ищем самый быстрый, а не ругаемся.
    if (!isZapret() && !findNode(state.selectedNodeId)) {
      fastest(true);
      return;
    }
    connect('');
  }

  // Самый быстрый сервер: пинг всех серверов, выбор лучшего, подключение.
  async function fastest(connectAfter) {
    if (fastestRunning || !state) return;
    if (!(state.subscriptions || []).length) {
      toast('Сначала добавьте подписку', 'err');
      openPage('subs');
      return;
    }
    fastestRunning = true;
    toast('Ищем самый быстрый сервер…');
    render();
    let best = null;
    try {
      state = await api('/api/nodes/ping', { method: 'POST', body: { subscriptionId: '', nodeIds: [] } });
      const engine = (state.settings || {}).engine || 'xray';
      const activeId = activeSubId();
      const candidates = allNodes().filter((n) => (!activeId || n.subId === activeId) && n.supported && n.pingStatus === 'ok' && n.pingMs > 0 &&
        !(engine === 'xray' && n.protocol === 'hysteria2'));
      candidates.sort((a, b) => a.pingMs - b.pingMs);
      best = candidates[0] || null;
      if (!best) {
        toast('Ни один сервер не ответил', 'err');
        return;
      }
      state = await api('/api/nodes/select', { method: 'POST', body: { nodeId: best.id } });
      toast('Самый быстрый: ' + nodeName(best) + ' · ' + best.pingMs + ' мс', 'ok');
    } catch (error) {
      toast(error.message, 'err');
      return;
    } finally {
      fastestRunning = false;
      render();
    }
    // Уже подключены к другому серверу — переключаемся на найденный.
    const current = state.connection || {};
    if (connectAfter && best && !(current.connected && current.nodeId === best.id)) connect(best.id);
  }

  $('fastest-btn').addEventListener('click', () => fastest(false));
  // Переключатель VPN / Запрет на главной.
  $('home-mode').addEventListener('click', (event) => {
    const button = event.target.closest('[data-home-mode]');
    if (!button || !state || button.dataset.homeMode === appMode) return;
    if (((state.zapret || {}).pick || {}).running) {
      toast('Дождитесь окончания подбора стратегии', 'err');
      return;
    }
    const next = button.dataset.homeMode;
    guard(async () => {
      state = await api('/api/settings', { method: 'POST', body: Object.assign({}, state.settings, { mode: next }) });
      toast(next === 'zapret' ? 'Режим «Запрет»' : 'Режим VPN', 'ok');
    });
  });

  $('zapret-strategy-tile').addEventListener('click', () => openStrategyModal(false));

  $('servers-add-sub').addEventListener('click', () => openPage('subs'));

  $('server-search').addEventListener('input', (event) => {
    search = event.target.value;
    renderServers();
  });

  $('server-list').addEventListener('click', (event) => {
    const infoBtn = event.target.closest('[data-info]');
    if (infoBtn) {
      event.stopPropagation();
      openServerModal(infoBtn.dataset.info);
      return;
    }
    const fav = event.target.closest('[data-fav]');
    if (fav) {
      event.stopPropagation();
      guard(async () => {
        await api('/api/nodes/favorite', { method: 'POST', body: { nodeId: fav.dataset.fav } });
        state = await api('/api/state');
      });
      return;
    }
    const row = event.target.closest('[data-node]');
    if (!row || row.classList.contains('is-off')) return;
    guard(async () => {
      state = await api('/api/nodes/select', { method: 'POST', body: { nodeId: row.dataset.node } });
      openPage('home');
    });
  });

  $('ping-btn').addEventListener('click', () => guard(async () => {
    $('ping-btn').disabled = true;
    try {
      state = await api('/api/nodes/ping', { method: 'POST', body: { subscriptionId: '', nodeIds: [] } });
      toast('Пинг проверен', 'ok');
    } finally {
      $('ping-btn').disabled = false;
    }
  }));

  // «Обновить» на странице серверов теперь действительно перезагружает
  // подписки, а не просто перечитывает локальное состояние.
  $('servers-refresh').addEventListener('click', () => guard(async () => {
    const ids = (state.subscriptions || []).map((sub) => sub.id);
    if (!ids.length) {
      toast('Сначала добавьте подписку', 'err');
      return;
    }
    await refreshSubscriptions(ids, $('servers-refresh'));
  }));

  $('home-sub-refresh').addEventListener('click', () => guard(async () => {
    const id = $('home-sub-card').dataset.sub;
    if (!id) {
      toast('Подписка не выбрана', 'err');
      return;
    }
    await refreshSubscriptions([id], $('home-sub-refresh'));
  }));

  $('sub-add').addEventListener('click', () => addSubscription($('sub-url').value.trim()));

  document.querySelectorAll('[data-bot]').forEach((button) => {
    button.addEventListener('click', () => openExternal(BOT_URL));
  });

  $('open-channel').addEventListener('click', () => {
    const url = (state && state.settings && state.settings.supportUrl) || BOT_URL;
    openExternal(url.startsWith('http') ? url : BOT_URL);
  });

  $('copy-id').addEventListener('click', async () => {
    const value = $('info-id').textContent;
    try {
      await navigator.clipboard.writeText(value);
      toast('Идентификатор скопирован', 'ok');
    } catch (_) {
      toast('Не удалось скопировать', 'err');
    }
  });

  $('sub-url').addEventListener('keydown', (event) => {
    if (event.key === 'Enter') $('sub-add').click();
  });

  $('subs-refresh-all').addEventListener('click', () => guard(async () => {
    const ids = (state.subscriptions || []).map((sub) => sub.id);
    if (!ids.length) {
      toast('Подписок пока нет', 'err');
      return;
    }
    await refreshSubscriptions(ids, $('subs-refresh-all'));
  }));

  $('sub-list').addEventListener('click', (event) => {
    const refreshBtn = event.target.closest('[data-sub-refresh]');
    if (refreshBtn) {
      guard(() => refreshSubscriptions([refreshBtn.dataset.subRefresh], refreshBtn));
      return;
    }
    const deleteBtn = event.target.closest('[data-sub-delete]');
    const row = event.target.closest('[data-sub-select]');
    if (!deleteBtn && row && !event.target.closest('button')) {
      selectSubscription(row.dataset.subSelect);
      return;
    }
    if (deleteBtn) {
      guard(async () => {
        state = await api('/api/subscriptions/delete', { method: 'POST', body: { id: deleteBtn.dataset.subDelete } });
        toast('Подписка удалена');
      });
    }
  });

  $('settings-save').addEventListener('click', () => guard(async () => {
    // Поля, которых нет на этой странице («Без VPN»), берутся из текущих
    // настроек: пропущенное поле программа поняла бы как «выключить».
    state = await api('/api/settings', {
      method: 'POST',
      body: Object.assign({}, state.settings, {
        autoPing: $('set-autoping').checked,
        serverSort: $('set-sort').value,
        mode: appMode,
        connectionMode: $('set-mode').value,
        engine: $('set-engine').value,
        autoRefresh: $('set-autorefresh').checked,
        autoConnect: $('set-autoconnect').checked,
        // Раньше здесь жёстко стояло false, поэтому галочка блокировки
        // рекламы сбрасывалась при каждом сохранении настроек.
        blockAds: $('set-blockads').checked,
        minimizeToTray: $('set-tray').checked,
        dns1: $('set-dns1').value.trim(),
        dns2: $('set-dns2').value.trim(),
        testUrl: $('set-testurl').value.trim(),
        pingMode: $('set-pingmode').value,
        pingTimeoutMs: Number($('set-pingtimeout').value) || 5000,
        consoleProxy: $('set-consoleproxy').checked,
        theme
      })
    });
    themeTouched = false;
    toast('Настройки сохранены', 'ok');
  }));

  // Тема применяется сразу по нажатию, чтобы было видно, что выбираешь.
  // В настройках она закрепится после «Сохранить».
  $('set-theme').addEventListener('click', (event) => {
    const button = event.target.closest('[data-theme-value]');
    if (!button) return;
    themeTouched = true;
    applyTheme(button.dataset.themeValue);
  });

  $('open-folder').addEventListener('click', () => guard(async () => {
    await api('/api/open-data-folder', { method: 'POST', body: {} });
  }));

  // Проверка и установка обновлений.
  //
  // Раньше кнопка «Обновить» открывала браузер, и дальше человек ставил
  // новую версию руками. Теперь программа скачивает установщик сама и
  // передаёт работу ему: сама себя переписать она не может — Windows не
  // даёт заменить работающий файл.
  let updateDownloadURL = '';
  let updatePollTimer = null;

  function showUpdateState(kind, title, sub) {
    const icon = $('update-icon');
    setText($('update-title'), title);
    setText($('update-sub'), sub);
    icon.className = 'update-icon' + (kind ? ' is-' + kind : '');
    $('update-download').hidden = kind !== 'new';
  }

  async function checkUpdate() {
    const button = $('update-check');
    button.disabled = true;
    const label = button.textContent;
    button.textContent = 'Проверяем…';
    try {
      const result = await api('/api/update/check', { method: 'POST', body: {} });
      updateDownloadURL = result.downloadUrl || '';
      if (result.hasUpdate) {
        showUpdateState('new', 'Доступна версия ' + result.latest,
          result.notes || ('Установлена ' + result.current));
        toast('Доступна версия ' + result.latest, 'ok');
      } else {
        showUpdateState('ok', 'Установлена последняя версия', 'Версия ' + result.current);
        toast('Обновлений нет', 'ok');
      }
    } catch (error) {
      showUpdateState('err', 'Не удалось проверить обновления', error.message);
      toast(error.message, 'err');
    } finally {
      button.disabled = false;
      button.textContent = label;
    }
  }

  function stopUpdatePolling() {
    if (updatePollTimer) {
      clearInterval(updatePollTimer);
      updatePollTimer = null;
    }
  }

  // Ход обновления спрашиваем у программы: скачивание идёт в фоне, запрос
  // на его запуск отвечает сразу и ничего не ждёт.
  function watchUpdateProgress() {
    stopUpdatePolling();
    updatePollTimer = setInterval(async () => {
      let progress;
      try {
        progress = await api('/api/update/progress');
      } catch (error) {
        return;
      }
      if (progress.stage === 'failed') {
        stopUpdatePolling();
        showUpdateState('err', 'Не удалось обновить', progress.error || progress.message || '');
        toast(progress.error || 'Не удалось обновить', 'err');
        restoreDownloadButton();
        return;
      }
      if (progress.stage === 'starting') {
        stopUpdatePolling();
        $('update-banner').hidden = true;
        showUpdateState('new', 'Запуск установщика',
          'Программа закроется, дальше всё сделает установщик.');
        return;
      }
      if (progress.message) {
        setText($('update-sub'), progress.message);
      }
    }, 500);
  }

  // Надпись на кнопке запоминаем и возвращаем как было: вёрстку интерфейса
  // трогать не нужно, текст берётся из неё.
  let downloadButtonLabel = '';

  function restoreDownloadButton() {
    const button = $('update-download');
    button.disabled = false;
    if (downloadButtonLabel) setText(button, downloadButtonLabel);
  }

  async function installUpdate() {
    const button = $('update-download');
    if (!downloadButtonLabel) downloadButtonLabel = button.textContent;
    button.disabled = true;
    setText(button, 'Обновляем…');
    showUpdateState('new', 'Обновление', 'Скачивание установщика…');
    try {
      await api('/api/update/install', { method: 'POST', body: {} });
      watchUpdateProgress();
    } catch (error) {
      stopUpdatePolling();
      showUpdateState('err', 'Не удалось обновить', error.message);
      toast(error.message, 'err');
      restoreDownloadButton();
    }
  }

  $('update-check').addEventListener('click', checkUpdate);
  $('update-download').addEventListener('click', installUpdate);

  /* ==================== полоса обновления ==================== */

  // Программа сама спрашивает сервер обновлений: через десять секунд после
  // запуска и дальше раз в шесть часов. Десять секунд - чтобы не мешать
  // подключению в первые мгновения.
  let bannerDismissed = false;

  function showUpdateBanner(version) {
    if (bannerDismissed) return;
    const banner = $('update-banner');
    setText($('ub-title'), 'Доступна версия ' + version);
    banner.hidden = false;
  }

  async function watchForUpdates() {
    if (bannerDismissed) return;
    try {
      const result = await api('/api/update/check', { method: 'POST', body: {} });
      updateDownloadURL = result.downloadUrl || '';
      if (result.hasUpdate) {
        showUpdateBanner(result.latest);
        showUpdateState('new', 'Доступна версия ' + result.latest,
          result.notes || ('Установлена ' + result.current));
      }
    } catch (error) {
      // Молча: нет сети или сервер недоступен - это не повод беспокоить
      // человека всплывашкой при каждом запуске.
    }
  }

  $('update-banner').addEventListener('click', (event) => {
    if (event.target.closest('#ub-close')) return;
    openPage('info');
  });

  $('ub-close').addEventListener('click', (event) => {
    event.stopPropagation();
    bannerDismissed = true;
    $('update-banner').hidden = true;
  });

  setTimeout(watchForUpdates, 10000);
  setInterval(watchForUpdates, 6 * 60 * 60 * 1000);

  async function loadLog() {
    try {
      const data = await api('/api/log');
      $('log-body').textContent = (data && data.log) ? data.log : 'Журнал пуст.';
    } catch (error) {
      $('log-body').textContent = error.message;
    }
  }
  $('log-refresh').addEventListener('click', loadLog);

  // Очистка журнала. Файл не удаляем, а обрезаем: он открыт на дозапись
  // самой программой, и удалённый файл продолжал бы «писаться» в никуда.
  $('log-clear').addEventListener('click', () => guard(async () => {
    await api('/api/log/clear', { method: 'POST', body: {} });
    events.length = 0;
    renderEvents();
    $('log-body').textContent = 'Журнал пуст.';
    toast('Журнал очищен', 'ok');
  }));
  renderEvents();

  /* ==================== режим «Запрет» ====================

     Своего экрана у режима нет: он живёт на той же главной и той же кнопке.
     Отличие в том, что показывать нечего — ни сервера, ни скорости: трафик
     идёт напрямую, мимо нас, и считать его нам нечем. Вместо этого две
     плитки: какая стратегия работает и сколько времени. */

  let appMode = 'vpn';
  // Включиться сразу после удачного подбора. Ставится только при первом
  // нажатии «Включить»: в остальных случаях человек пришёл в подбор сам и
  // решает сам.
  let enableAfterPick = false;
  let modalOpen = false;
  let strategiesLoading = false;
  let strategyCatalog = null;

  const isZapret = () => appMode === 'zapret';

  function renderZapret() {
    const settings = state.settings || {};
    appMode = settings.mode === 'zapret' ? 'zapret' : 'vpn';
    const zapret = state.zapret || {};
    const connection = state.connection || {};
    const on = !!connection.connected;

    $('home-title').hidden = !isZapret();
    $('server-chip').hidden = isZapret();
    $('speed-row').hidden = isZapret();
    $('zapret-row').hidden = !isZapret();
    const seg = $('home-mode');
    seg.classList.toggle('is-zapret', isZapret());
    for (const button of seg.querySelectorAll('[data-home-mode]')) {
      button.setAttribute('aria-checked', String(button.dataset.homeMode === appMode));
    }
    seg.hidden = !zapret.available && !isZapret();
    if (isZapret()) {
      $('home-sub-card').hidden = true;
      setText($('zapret-strategy'), zapret.strategyName || '—');
      setText($('power-label'), on ? 'Выключить' : connection.connecting ? 'Включение…' : 'Включить');
      setText($('status-text'), on ? 'Включено' : connection.connecting ? 'Включение…' : 'Выключено');
      setText($('power-state'), on ? 'Запрет включён' : connection.connecting ? 'Включение' : 'Запрет выключен');
    }
    renderPickProgress();
  }

  // Карточки, которые в «Запрете» ничего не делают.
  //
  // Шифрованный DNS здесь не работает: DoH заворачивался в туннель правилом
  // Xray, а в этом режиме Xray не запускается вообще. Написать «работает
  // через DoH» было бы неправдой, поэтому карточка гасится с подписью.
  const naFields = [
    ['field-engine', 'Только для VPN'],
    ['field-connection-mode', 'Только для VPN'],
    ['field-dns', 'Только для TUN'],
    ['field-blockads', 'Только для VPN']
  ];

  function renderZapretSettings() {
    const zapret = state.zapret || {};
    for (const button of $('set-app-mode').querySelectorAll('[data-mode-value]')) {
      button.setAttribute('aria-checked', String(button.dataset.modeValue === appMode));
    }
    for (const [id, label] of naFields) {
      const node = $(id);
      if (!node) continue;
      node.classList.toggle('is-na', isZapret());
      let mark = node.querySelector('.na-mark');
      if (isZapret()) {
        if (!mark) {
          mark = document.createElement('span');
          mark.className = 'na-mark';
          const target = node.querySelector('span') || node;
          target.appendChild(mark);
        }
        setText(mark, '· ' + label);
      } else if (mark) {
        mark.remove();
      }
    }

    // Подпись под плитками описывает выбранный режим, а не всегда «Запрет»:
    // иначе в режиме VPN человек читал бы предупреждение про блокировки по IP,
    // которые его в этот момент не касаются.
    setText($('mode-hint'), isZapret()
      ? 'Сервер не нужен, подписка не тратится. Сайты, заблокированные по IP, «Запрет» не открывает — для них нужен VPN.'
      : 'Весь трафик идёт через выбранный сервер. Открывает всё, в том числе заблокированное по IP.');

    $('zapret-settings').hidden = !isZapret();
    if (!isZapret()) return;

    const pick = zapret.pick || {};
    if (!zapret.available) {
      setText($('zapret-settings-sub'), 'Файлы режима не найдены рядом с программой — переустановите MS7VPN');
      $('zapret-pick').disabled = true;
      $('zapret-choose').disabled = true;
      return;
    }
    $('zapret-pick').disabled = !!pick.running;
    $('zapret-choose').disabled = false;
    setText($('zapret-pick'), zapret.picked ? 'Подобрать заново' : 'Подобрать');
    if (pick.running) {
      setText($('zapret-settings-sub'), 'Подбор: ' + pick.index + ' из ' + pick.total + ' — ' + (pick.current || ''));
      return;
    }
    const marks = describeResult(zapret.strategyId);
    setText($('zapret-settings-sub'), (zapret.strategyName || '—') + (marks ? ' · ' + marks : ''));
  }

  // Что мы знаем про стратегию по итогам подбора.
  function describeResult(id) {
    if (!id || !strategyCatalog) return '';
    const info = strategyCatalog.find((item) => item.id === id);
    if (!info || !info.checked) return '';
    return 'YouTube ' + (info.youtube ? '✓' : '✗') + ' · Discord ' + (info.discord ? '✓' : '✗');
  }

  /* ==== окно подбора и выбора ==== */

  async function loadStrategies() {
    if (strategiesLoading) return;
    strategiesLoading = true;
    try {
      const data = await api('/api/zapret/strategies');
      strategyCatalog = (data && data.strategies) || [];
      renderStrategyList();
    } catch (error) {
      $('strategy-list').innerHTML = '<span class="events-empty">' + escapeHtml(error.message) + '</span>';
    } finally {
      strategiesLoading = false;
    }
  }

  function renderStrategyList() {
    const list = $('strategy-list');
    if (!strategyCatalog) return;
    const current = ((state && state.zapret) || {}).strategyId;
    list.innerHTML = strategyCatalog.map((info) => {
      const marks = info.checked
        ? '<span class="mark ' + (info.youtube ? 'is-ok' : 'is-no') + '">YouTube ' + (info.youtube ? '✓' : '✗') + '</span>' +
          '<span class="mark ' + (info.discord ? 'is-ok' : 'is-no') + '">Discord ' + (info.discord ? '✓' : '✗') + '</span>'
        : '<span class="mark">не проверялась</span>';
      return '<div class="strategy' + (info.id === current ? ' is-selected' : '') + '" data-strategy="' + escapeHtml(info.id) + '">' +
        '<span class="strategy-name">' + escapeHtml(info.name) + '</span>' +
        '<span class="strategy-marks">' + marks + '</span></div>';
    }).join('');
  }

  function renderPickProgress() {
    const zapret = (state && state.zapret) || {};
    const pick = zapret.pick || {};
    const box = $('pick-progress');
    box.hidden = !pick.running && !pick.done;
    if (pick.running) {
      const percent = pick.total ? Math.round((pick.index / pick.total) * 100) : 0;
      $('pick-bar').style.width = percent + '%';
      setText($('pick-text'), 'Проверяю ' + pick.index + ' из ' + pick.total + ': ' + (pick.current || ''));
    } else if (pick.done) {
      $('pick-bar').style.width = '100%';
      if (pick.error) {
        setText($('pick-text'), pick.error);
      } else if (pick.found) {
        setText($('pick-text'), pick.full
          ? 'Подошла стратегия «' + pick.found + '» — открылись и YouTube, и Discord'
          : 'Лучшая из найденных: «' + pick.found + '». Полностью не помогла ни одна — выберите, что важнее');
      }
    }
    $('pick-stop').hidden = !pick.running;
    $('pick-start').disabled = !!pick.running;
    setText($('pick-start'), zapret.picked ? 'Подобрать заново' : 'Подобрать');
  }

  function openStrategyModal(startPick) {
    modalOpen = true;
    $('zapret-modal').hidden = false;
    loadStrategies();
    renderPickProgress();
    if (startPick) startAutoPick();
  }

  function closeStrategyModal() {
    modalOpen = false;
    enableAfterPick = false;
    $('zapret-modal').hidden = true;
  }

  async function startAutoPick() {
    await guard(async () => {
      await api('/api/zapret/pick', { method: 'POST', body: {} });
      state = await api('/api/state');
      toast('Подбор начат: это займёт несколько минут', 'ok');
    });
  }

  $('zapret-choose').addEventListener('click', () => openStrategyModal(false));
  $('zapret-pick').addEventListener('click', () => openStrategyModal(true));
  $('pick-start').addEventListener('click', () => startAutoPick());
  $('zapret-close').addEventListener('click', closeStrategyModal);
  $('zapret-done').addEventListener('click', closeStrategyModal);
  $('zapret-modal').addEventListener('click', (event) => {
    if (event.target === $('zapret-modal')) closeStrategyModal();
  });

  // «Остановить» обрывает подбор сразу: отмена идёт контекстом на стороне
  // программы, winws снимается не дожидаясь конца текущей проверки.
  $('pick-stop').addEventListener('click', () => guard(async () => {
    await api('/api/zapret/pick/stop', { method: 'POST', body: {} });
    state = await api('/api/state');
    toast('Подбор остановлен', 'ok');
  }));

  // Строка выбирается кликом и сохраняется сразу. Если обход включён, он
  // переподнимается с новой стратегией — это делает программа.
  $('strategy-list').addEventListener('click', (event) => {
    const row = event.target.closest('[data-strategy]');
    if (!row) return;
    guard(async () => {
      state = await api('/api/zapret/select', { method: 'POST', body: { id: row.dataset.strategy } });
      renderStrategyList();
      toast('Стратегия выбрана', 'ok');
    });
  });

  // Переключатель защиты. Сохраняется сразу, а не по «Сохранить»: смена
  // режима на включённом соединении переподключает, и ждать отдельного
  // нажатия здесь незачем.
  $('set-app-mode').addEventListener('click', (event) => {
    const button = event.target.closest('[data-mode-value]');
    if (!button || button.dataset.modeValue === appMode) return;
    appMode = button.dataset.modeValue;
    renderZapretSettings();
    guard(async () => {
      state = await api('/api/settings', {
        method: 'POST',
        body: Object.assign({}, state.settings, { mode: appMode })
      });
      toast(isZapret() ? 'Включён режим «Запрет»' : 'Включён режим VPN', 'ok');
    });
  });

  // Итог подбора: окно закрывается само и обход включается только если
  // стратегия открыла обе группы сайтов. Помогла частично — окно остаётся,
  // чтобы человек выбрал, что ему важнее.
  let pickFollowed = false;

  function followPick() {
    if (!state) return;
    const pick = (state.zapret || {}).pick || {};
    if (!pick.done) {
      pickFollowed = false;
      return;
    }
    if (pickFollowed) return;
    pickFollowed = true;
    if (modalOpen) loadStrategies();
    if (!enableAfterPick) return;
    enableAfterPick = false;
    if (pick.full && pick.foundId) {
      closeStrategyModal();
      connect('');
    }
  }

  /* ==================== «Без VPN» ==================== */

  let currentPage = 'home';
  $('split-hero-flag').innerHTML = flags.flagHTML('Россия');
  let splitDirty = false;

  const linesOf = (text) => text.split(/[\r\n,;]+/).map((s) => s.trim()).filter(Boolean);

  function renderSplit(force) {
    if (!state) return;
    const settings = state.settings || {};
    // Пока человек правит списки, опрос состояния их не перетирает.
    if (!force && splitDirty) return;
    // Не трогаем поле, в котором стоит курсор.
    const active = document.activeElement;
    if (!force && active && active.closest && active.closest('#page-split')) return;
    $('split-ru').checked = !!settings.directRu;
    $('split-direct').value = (settings.directDomains || []).join('\n');
    $('split-proxy').value = (settings.proxyDomains || []).join('\n');
    $('split-apps').value = (settings.directApps || []).join('\n');
    splitDirty = false;
    renderSplitCounts();
    renderPopularApps();
  }

  function renderSplitCounts() {
    const count = (id) => linesOf($(id).value).length;
    setText($('split-direct-count'), String(count('split-direct')));
    setText($('split-proxy-count'), String(count('split-proxy')));
    setText($('split-apps-count'), String(count('split-apps')));
    $('split-hero').classList.toggle('is-on', $('split-ru').checked);
    const settings = (state && state.settings) || {};
    const tun = settings.connectionMode === 'tun';
    const note = $('split-apps-note');
    setText(note, tun ? 'Работает в режиме TUN' : 'Только в режиме TUN — сейчас системный прокси');
    note.className = 'split-note' + (tun ? '' : ' is-warn');
  }

  ['split-direct', 'split-proxy', 'split-apps'].forEach((id) => {
    $(id).addEventListener('input', () => { splitDirty = true; renderSplitCounts(); });
  });
  $('split-ru').addEventListener('change', () => { splitDirty = true; renderSplitCounts(); });

  $('split-save').addEventListener('click', () => guard(async () => {
    state = await api('/api/settings', {
      method: 'POST',
      body: Object.assign({}, state.settings, {
        directRu: $('split-ru').checked,
        directDomains: linesOf($('split-direct').value),
        proxyDomains: linesOf($('split-proxy').value),
        directApps: linesOf($('split-apps').value)
      })
    });
    splitDirty = false;
    renderSplit(true);
    toast(state.connection.connecting ? 'Сохранено — переподключаемся с новыми правилами' : 'Правила сохранены', 'ok');
  }));

  /* ==================== статистика ==================== */

  let usage = null;

  async function loadUsage() {
    try {
      usage = await api('/api/usage');
      renderUsage();
    } catch (_) {
      // Статистика — украшение: без неё всё остальное работает.
    }
  }

  function formatDuration(seconds) {
    const total = Math.max(0, Math.floor(Number(seconds) || 0));
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    if (h >= 1) return `${h} ч ${String(m).padStart(2, '0')} мин`;
    return `${m} мин`;
  }

  function sumDays(days) {
    return days.reduce((acc, d) => ({
      bytes: acc.bytes + d.up + d.down,
      seconds: acc.seconds + d.seconds,
      connections: acc.connections + d.connections
    }), { bytes: 0, seconds: 0, connections: 0 });
  }

  function trafficHTML(bytes) {
    const text = formatTraffic(bytes);
    const parts = text.split(' ');
    return escapeHtml(parts[0]) + '<small>' + escapeHtml(parts[1] || '') + '</small>';
  }

  function renderUsage() {
    if (!usage) return;
    const days = usage.days || [];
    const today = sumDays(days.slice(-1));
    const week = sumDays(days.slice(-7));
    const month = sumDays(days);
    if (currentPage !== 'stats') return;

    $('st-today').innerHTML = trafficHTML(today.bytes);
    $('st-week').innerHTML = trafficHTML(week.bytes);
    $('st-month').innerHTML = trafficHTML(month.bytes);
    setText($('st-today-time'), formatDuration(today.seconds) + ' · ' + today.connections + ' ' + plural(today.connections, 'подключение', 'подключения', 'подключений'));
    setText($('st-week-time'), formatDuration(week.seconds) + ' в VPN');
    setText($('st-month-time'), formatDuration(month.seconds) + ' в VPN');

    const total = usage.total || {};
    setText($('st-conn'), String(total.connections || 0));
    setText($('st-time'), formatDuration(total.seconds));
    setText($('st-ping'), total.avgPing ? total.avgPing + ' мс' : '—');
    setText($('st-all'), formatTraffic((total.up || 0) + (total.down || 0)));

    const last = days.slice(-14);
    const max = Math.max(1, ...last.map((d) => d.up + d.down));
    const weekdays = ['вс', 'пн', 'вт', 'ср', 'чт', 'пт', 'сб'];
    $('st-days').innerHTML = last.map((d, i) => {
      const date = new Date(d.date + 'T12:00:00');
      const label = i === last.length - 1 ? 'сегодня' : weekdays[date.getDay()] + ' ' + date.getDate();
      const downH = (d.down / max) * 100;
      const upH = (d.up / max) * 100;
      const value = d.up + d.down > 0 ? formatTraffic(d.up + d.down) : '';
      const title = `${date.toLocaleDateString('ru-RU', { day: 'numeric', month: 'long' })}: ↓ ${formatTraffic(d.down)}, ↑ ${formatTraffic(d.up)}, ${formatDuration(d.seconds)}`;
      return `<div class="day${i === last.length - 1 ? ' is-today' : ''}" title="${escapeHtml(title)}">
        <span class="day-val">${escapeHtml(value)}</span>
        <span class="day-bars"><i class="b-down" style="height:${downH.toFixed(1)}%"></i><i class="b-up" style="height:${upH.toFixed(1)}%"></i></span>
        <span class="day-label">${escapeHtml(label)}</span>
      </div>`;
    }).join('');
  }

  $('stats-reset').addEventListener('click', () => guard(async () => {
    await api('/api/usage', { method: 'DELETE' });
    await loadUsage();
    toast('Статистика очищена', 'ok');
  }));

  /* ==================== DNS и сброс настроек ==================== */

  function renderDnsChips() {
    const pair = [$('set-dns1').value.trim(), $('set-dns2').value.trim()].join(',');
    for (const chip of $('dns-presets').querySelectorAll('[data-dns]')) {
      chip.classList.toggle('is-on', chip.dataset.dns === pair);
    }
  }

  $('dns-presets').addEventListener('click', (event) => {
    const chip = event.target.closest('[data-dns]');
    if (!chip) return;
    const [dns1, dns2] = chip.dataset.dns.split(',');
    $('set-dns1').value = dns1;
    $('set-dns2').value = dns2;
    renderDnsChips();
    toast('DNS: ' + chip.textContent + ' — нажмите «Сохранить»');
  });
  ['set-dns1', 'set-dns2'].forEach((id) => $(id).addEventListener('input', renderDnsChips));

  // Сброс в два нажатия: первое меняет надпись, второе — сбрасывает.
  let resetArmed = null;
  $('settings-reset').addEventListener('click', () => {
    const button = $('settings-reset');
    if (!resetArmed) {
      button.textContent = 'Точно сбросить?';
      resetArmed = setTimeout(() => { resetArmed = null; button.textContent = 'Сбросить настройки'; }, 3000);
      return;
    }
    clearTimeout(resetArmed);
    resetArmed = null;
    button.textContent = 'Сбросить настройки';
    guard(async () => {
      state = await api('/api/settings/reset', { method: 'POST', body: {} });
      themeTouched = false;
      applyTheme(state.settings.theme);
      renderSplit(true);
      toast('Настройки сброшены. Подписки остались на месте', 'ok');
    });
  });

  /* ==================== Ctrl+V, Esc ==================== */

  // Ссылка подписки из буфера — с любого экрана, без поиска поля ввода.
  function looksLikeSubscription(text) {
    return /^(https?:\/\/|vless:\/\/|vmess:\/\/|trojan:\/\/|ss:\/\/|hy2:\/\/|hysteria2:\/\/)\S+$/i.test(text);
  }

  document.addEventListener('paste', (event) => {
    const target = event.target;
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return;
    const text = ((event.clipboardData && event.clipboardData.getData('text')) || '').trim();
    if (!text) return;
    event.preventDefault();
    if (!looksLikeSubscription(text)) {
      toast('В буфере не ссылка подписки', 'err');
      return;
    }
    const shell = $('shell');
    shell.classList.remove('paste-flash');
    void shell.offsetWidth;
    shell.classList.add('paste-flash');
    addSubscription(text);
  });

  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    if (modalOpen) closeStrategyModal();
    if (!$('srv-modal').hidden) closeServerModal();
  });

  /* ==================== окно сервера ==================== */

  let serverModalId = '';

  function openServerModal(id) {
    const node = findNode(id);
    if (!node) return;
    serverModalId = id;
    $('srv-flag').innerHTML = flags.flagHTML(node.name);
    setText($('srv-name'), nodeName(node));
    $('srv-rename').value = (state.nodeNames || {})[id] || '';
    $('srv-rename').placeholder = flags.cleanName(node.name);
    const sub = (state.subscriptions || []).find((s) => s.id === node.subId);
    const rows = [
      ['Название в подписке', flags.cleanName(node.name)],
      ['Протокол', String(node.protocol || '—').toUpperCase()],
      ['Адрес', node.address + (node.port ? ':' + node.port : '')],
      ['Транспорт', node.transport || 'tcp'],
      ['Защита', node.security || 'нет'],
      ['SNI', node.sni || '—'],
      ['Пинг', pingText(node)],
      ['Подписка', sub ? (sub.name || 'Подписка') : '—'],
      ['Поддержка', node.supported ? 'Да' : (node.unsupportedReason || 'Нет')]
    ];
    $('srv-details').innerHTML = rows.map(([k, v]) =>
      `<div class="info-row"><span>${escapeHtml(k)}</span><b title="${escapeHtml(v)}">${escapeHtml(v)}</b></div>`).join('');
    $('srv-connect').disabled = !node.supported;
    $('srv-modal').hidden = false;
  }

  function closeServerModal() {
    $('srv-modal').hidden = true;
    serverModalId = '';
  }

  $('srv-close').addEventListener('click', closeServerModal);
  $('srv-modal').addEventListener('click', (event) => { if (event.target === $('srv-modal')) closeServerModal(); });

  async function saveServerName() {
    const id = serverModalId;
    if (!id) return;
    await guard(async () => {
      state = await api('/api/nodes/rename', { method: 'POST', body: { nodeId: id, name: $('srv-rename').value } });
      toast($('srv-rename').value.trim() ? 'Название сохранено' : 'Вернули название из подписки', 'ok');
    });
    if (serverModalId === id) openServerModal(id);
  }
  $('srv-rename-save').addEventListener('click', saveServerName);
  $('srv-rename').addEventListener('keydown', (event) => { if (event.key === 'Enter') saveServerName(); });

  $('srv-copy').addEventListener('click', async () => {
    const node = findNode(serverModalId);
    if (!node) return;
    try {
      await navigator.clipboard.writeText(node.rawUri || '');
      toast('Ссылка сервера скопирована', 'ok');
    } catch (_) {
      toast('Не удалось скопировать', 'err');
    }
  });

  $('srv-ping').addEventListener('click', () => {
    const id = serverModalId;
    guard(async () => {
      state = await api('/api/nodes/ping', { method: 'POST', body: { subscriptionId: '', nodeIds: [id] } });
    }).then(() => { if (serverModalId === id) openServerModal(id); });
  });

  $('srv-connect').addEventListener('click', () => {
    const id = serverModalId;
    closeServerModal();
    guard(async () => {
      state = await api('/api/nodes/select', { method: 'POST', body: { nodeId: id } });
    }).then(() => { openPage('home'); connect(id); });
  });

  /* ==================== ссылки ms7vpn:// ==================== */

  let pendingLinkURL = '';
  let linkTaking = false;

  async function takeDeepLink() {
    if (linkTaking || pendingLinkURL) return;
    linkTaking = true;
    try {
      const data = await api('/api/deeplink/take', { method: 'POST', body: {} });
      if (data && data.url) {
        pendingLinkURL = data.url;
        let host = data.url;
        try { host = new URL(data.url).host; } catch (_) { /* как есть */ }
        setText($('link-bar-url'), host);
        $('link-bar').title = data.url;
        $('link-bar').hidden = false;
      }
    } catch (_) {
      // Старая версия программы без этого метода — молча.
    } finally {
      linkTaking = false;
    }
  }

  $('link-bar-yes').addEventListener('click', () => {
    const url = pendingLinkURL;
    pendingLinkURL = '';
    $('link-bar').hidden = true;
    addSubscription(url);
  });
  $('link-bar-no').addEventListener('click', () => {
    pendingLinkURL = '';
    $('link-bar').hidden = true;
  });

  /* ==================== автозапуск и размер текста ==================== */

  api('/api/autostart').then((data) => { $('set-autostart').checked = !!(data && data.enabled); }).catch(() => {});
  $('set-autostart').addEventListener('change', async (event) => {
    const enabled = event.target.checked;
    try {
      const data = await api('/api/autostart', { method: 'POST', body: { enabled } });
      event.target.checked = !!data.enabled;
      toast(data.enabled ? 'MS7VPN будет запускаться вместе с Windows' : 'Автозапуск выключен', 'ok');
    } catch (error) {
      event.target.checked = !enabled;
      toast(error.message, 'err');
    }
  });

  function applyFont(size) {
    const value = ['small', 'large'].includes(size) ? size : 'normal';
    if (value === 'normal') document.documentElement.removeAttribute('data-font');
    else document.documentElement.setAttribute('data-font', value);
    for (const chip of $('font-size').querySelectorAll('[data-font]')) {
      chip.classList.toggle('is-on', chip.dataset.font === value);
    }
    try { localStorage.setItem('ms7-font', value); } catch (_) { /* не запомнится */ }
    requestAnimationFrame(drawChart);
  }
  try { applyFont(localStorage.getItem('ms7-font')); } catch (_) { applyFont('normal'); }
  $('font-size').addEventListener('click', (event) => {
    const chip = event.target.closest('[data-font]');
    if (chip) applyFont(chip.dataset.font);
  });

  /* ==================== выбор программ для «Без VPN» ====================

     Список программ — плитки со значками. Значок берётся из самого exe
     (программа находит его по имени: среди запущенных, в реестре и в папках
     установки). Не нашли — фирменный значок популярной программы или буква. */

  const APP_BRANDS = window.MS7AppIcons || [];
  const brandFor = (exe) => APP_BRANDS.find((b) => b.exe === exe.toLowerCase().split('\\').pop());

  function appIconHTML(exe) {
    const brand = brandFor(exe);
    const fallback = brand
      ? `<span class="app-fb" style="background:${brand.hex}"><svg viewBox="0 0 24 24"><path d="${brand.d}"/></svg></span>`
      : `<span class="app-fb app-letter">${escapeHtml(exe.split('\\').pop().slice(0, 1).toUpperCase())}</span>`;
    const src = '/api/appicon?name=' + encodeURIComponent(exe) + '&token=' + encodeURIComponent(TOKEN);
    return `<span class="app-ic"><img class="app-img" src="${escapeHtml(src)}" alt="" loading="lazy">${fallback}</span>`;
  }

  // Значок не нашёлся — показываем запасной.
  document.addEventListener('error', (event) => {
    const img = event.target;
    if (img && img.classList && img.classList.contains('app-img')) img.parentElement.classList.add('is-fb');
  }, true);

  function appDisplayName(exe) {
    const brand = brandFor(exe);
    if (brand) return brand.name;
    const base = exe.split('\\').pop().replace(/\.exe$/i, '');
    return base.charAt(0).toUpperCase() + base.slice(1);
  }

  function appLines() { return linesOf($('split-apps').value); }
  const sameApp = (a, b) => a.toLowerCase().split('\\').pop() === b.toLowerCase().split('\\').pop();

  function renderAppsList() {
    const apps = appLines();
    $('apps-list').innerHTML = apps.length
      ? apps.map((exe) => `<span class="app-tag" title="${escapeHtml(exe)}">${appIconHTML(exe)}<span class="app-name">${escapeHtml(appDisplayName(exe))}</span><button type="button" class="app-x" data-remove-app="${escapeHtml(exe)}" aria-label="Убрать">×</button></span>`).join('')
      : '<span class="events-empty">Пока ни одной — выберите из популярных или запущенных.</span>';
  }

  function renderPopularApps() {
    const apps = appLines();
    $('apps-pop').innerHTML = APP_BRANDS.map((brand) => {
      const on = apps.some((a) => sameApp(a, brand.exe));
      return `<button type="button" class="app-chip${on ? ' is-on' : ''}" data-app="${escapeHtml(brand.exe)}" title="${escapeHtml(brand.exe)}">${appIconHTML(brand.exe)}<span class="app-name">${escapeHtml(brand.name)}</span>${on ? '<i class="app-check">✓</i>' : ''}</button>`;
    }).join('');
    renderAppsList();
  }

  function setApps(list) {
    $('split-apps').value = list.join('\n');
    splitDirty = true;
    renderSplitCounts();
    renderPopularApps();
    renderProcesses();
  }

  function toggleApp(exe, forceOn) {
    const apps = appLines();
    const index = apps.findIndex((a) => sameApp(a, exe));
    if (index >= 0 && !forceOn) apps.splice(index, 1);
    else if (index < 0) apps.push(exe);
    setApps(apps);
  }

  $('apps-pop').addEventListener('click', (event) => {
    const chip = event.target.closest('[data-app]');
    if (chip) toggleApp(chip.dataset.app, false);
  });
  $('apps-list').addEventListener('click', (event) => {
    const remove = event.target.closest('[data-remove-app]');
    if (remove) toggleApp(remove.dataset.removeApp, false);
  });

  function addTypedApp() {
    const value = $('app-input').value.trim().replace(/^"|"$/g, '');
    if (!value) { toast('Введите имя программы, например game.exe'); return; }
    toggleApp(/\.exe$/i.test(value) ? value : value + '.exe', true);
    $('app-input').value = '';
  }
  $('app-add').addEventListener('click', addTypedApp);
  $('app-input').addEventListener('keydown', (event) => { if (event.key === 'Enter') addTypedApp(); });

  let runningApps = [];

  function renderProcesses() {
    const box = $('apps-procs');
    if (box.hidden) return;
    const apps = appLines();
    box.innerHTML = runningApps.length
      ? runningApps.map((exe) => {
        const on = apps.some((a) => sameApp(a, exe));
        return `<button type="button" class="app-chip${on ? ' is-on' : ''}" data-proc="${escapeHtml(exe)}" title="${escapeHtml(exe)}">${appIconHTML(exe)}<span class="app-name">${escapeHtml(appDisplayName(exe))}</span>${on ? '<i class="app-check">✓</i>' : ''}</button>`;
      }).join('')
      : '<span class="events-empty">Загружаем список…</span>';
  }

  async function loadProcesses() {
    try {
      const data = await api('/api/processes');
      runningApps = (data && data.processes) || [];
    } catch (error) {
      toast(error.message, 'err');
    }
    renderProcesses();
  }

  $('proc-toggle').addEventListener('click', () => {
    const box = $('apps-procs');
    box.hidden = !box.hidden;
    setText($('proc-toggle'), box.hidden ? 'Из запущенных' : 'Скрыть запущенные');
    if (!box.hidden) { renderProcesses(); loadProcesses(); }
  });
  $('apps-procs').addEventListener('click', (event) => {
    const chip = event.target.closest('[data-proc]');
    if (chip) toggleApp(chip.dataset.proc, false);
  });

  /* ==================== фон и частота опроса ==================== */

  // Свёрнутое окно не тратит процессор на анимации.
  function updatePaused() {
    document.documentElement.classList.toggle('paused', document.visibilityState !== 'visible');
  }
  document.addEventListener('visibilitychange', updatePaused);
  updatePaused();

  // Пока VPN включён и видна главная, состояние опрашивается чаще — ради
  // плавного графика скорости. В остальное время хватает прежних 2,5 с.
  let pollTimer = null;
  let usageTick = 0;
  function schedulePoll() {
    clearTimeout(pollTimer);
    const live = state && state.connection && state.connection.connected && currentPage === 'home' && document.visibilityState === 'visible';
    pollTimer = setTimeout(async () => {
      await refresh();
      pushSpeedSample();
      takeDeepLink();
      if (++usageTick % 12 === 0 || currentPage === 'stats') loadUsage();
      schedulePoll();
    }, live ? 1000 : 2500);
  }

  /* ==================== запуск ==================== */

  api('/api/device').then((data) => { device = data; renderInfo(); }).catch(() => {});
  refresh().then(() => {
    renderSplit(true);
    drawChart();
    loadUsage();
    // Автопинг при открытии — как у мобильных клиентов. Молча: результат
    // виден в списке и на значке сервера.
    if (state && state.settings && state.settings.autoPing && allNodes().length && !state.connection.connecting) {
      api('/api/nodes/ping', { method: 'POST', body: { subscriptionId: '', nodeIds: [] } })
        .then((next) => { if (!busy) { state = next; render(); } })
        .catch(() => {});
    }
  });
  schedulePoll();
  timerHandle = setInterval(updateTimer, 1000);
  // Страница из адреса окна (?page=stats) — для открытия из трея и проверки вида.
  const START_PAGE = new URLSearchParams(location.search).get('page');
  if (START_PAGE && document.querySelector('.nav-item[data-page="' + START_PAGE.replace(/[^a-z]/g, '') + '"]')) openPage(START_PAGE.replace(/[^a-z]/g, ''));
  window.addEventListener('beforeunload', () => clearInterval(timerHandle));

  // Маячок: сообщаем программе, что страница ожила. Без него программа не
  // отличала «страница отдана» от «страница работает».
  //
  // Сначала маячок ждал двух кадров requestAnimationFrame. Так надёжнее по
  // смыслу, но WebView2 не выдаёт кадры, пока окно не начало показываться, —
  // получался замкнутый круг: программа считала окно мёртвым и переходила
  // заново, обрывая отрисовку. Поэтому кадр остаётся быстрым путём, а
  // страховкой идёт таймер. Повторный вызов на стороне программы безвреден.
  let readySent = false;
  function reportReady() {
    if (readySent) return;
    readySent = true;
    api('/api/ui-ready', { method: 'POST', body: {} }).catch(() => {});
  }
  requestAnimationFrame(() => requestAnimationFrame(reportReady));
  setTimeout(reportReady, 1200);
})();
