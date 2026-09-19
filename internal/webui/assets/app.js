/* MS7VPN — интерфейс окна. Вся логика остаётся в Go, страница только рисует
   состояние и дёргает локальный API. */
(() => {
  'use strict';

  const TOKEN = window.MS7_TOKEN || new URLSearchParams(location.search).get('token') || '';
  const AUTO_CONNECT = new URLSearchParams(location.search).get('autoconnect') || '';
  const BOT_URL = 'https://t.me/ms7vpn_bot';
  const flags = window.MS7Flags;
  let device = null;

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
    power.className = 'power' + (connected ? ' is-on' : connecting ? ' is-wait' : '');
    power.disabled = connecting;
    $('power-label').textContent = connected ? 'ОТКЛЮЧИТЬ' : connecting ? 'ПОДКЛЮЧЕНИЕ…' : 'ПОДКЛЮЧИТЬ';

    const selected = findNode(state.selectedNodeId);
    $('chip-name').textContent = selected ? flags.cleanName(selected.name) : 'Сервер не выбран';
    $('chip-flag').innerHTML = selected ? flags.flagHTML(selected.name) : '';

    renderSpeed(connection, connected);

    const stats = $('home-stats');
    stats.hidden = !connected;
    if (connected) {
      setText($('stat-mode'), connection.mode === 'tun' ? 'TUN — весь трафик' : 'Системный прокси');
    }

    const notice = $('home-notice');
    const problem = connection.lastError || (state.core && state.core.lastError) || '';
    const warning = (connection.warnings || []).join('\n');
    const info = connecting ? (connection.statusMessage || '') : '';
    notice.className = 'notice';
    if (problem) {
      notice.hidden = false;
      notice.textContent = problem;
    } else if (warning) {
      notice.hidden = false;
      notice.className = 'notice is-warn';
      notice.textContent = warning;
    } else if (info) {
      notice.hidden = false;
      notice.textContent = info;
    } else {
      notice.hidden = true;
    }

    renderServers();
    renderHomeSubscription();
    renderSubs();
    renderSettings();
    renderInfo();
    updateWelcome();
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
    setText(row.querySelector('.row-title'), flags.cleanName(node.name));
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

    const visible = allNodes().filter((node) => !needle || (node.name || '').toLowerCase().includes(needle));
    visible.sort((a, b) => {
      const favDiff = (favorites[b.id] ? 1 : 0) - (favorites[a.id] ? 1 : 0);
      if (favDiff) return favDiff;
      const aPing = a.pingStatus === 'ok' ? a.pingMs : 99999;
      const bPing = b.pingStatus === 'ok' ? b.pingMs : 99999;
      if (aPing !== bPing) return aPing - bPing;
      return (a.name || '').localeCompare(b.name || '', 'ru');
    });

    $('servers-empty').hidden = visible.length > 0;

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
    const downHTML = formatSpeed(connected ? connection.downlinkBitsSec : 0);
    const upHTML = formatSpeed(connected ? connection.uplinkBitsSec : 0);
    if (down.innerHTML !== downHTML) down.innerHTML = downHTML;
    if (up.innerHTML !== upHTML) up.innerHTML = upHTML;
    $('speed-row').classList.toggle('is-idle', !connected);
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
    const current = subs.find((sub) => sub.id === (selected ? selected.subId : state.selectedSubscriptionId)) || subs[0];
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
    if (info.expireUnix) {
      expireRow.hidden = false;
      setText($('home-sub-expire'),
        new Date(info.expireUnix * 1000).toLocaleDateString('ru-RU', { day: 'numeric', month: 'short', year: 'numeric' }));
    } else {
      expireRow.hidden = true;
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

    const count = (current.nodes || []).length;
    setText($('home-sub-hint'),
      `${count} ${plural(count, 'сервер', 'сервера', 'серверов')} · обновлено ${formatDate(current.updatedAt)}`);
  }

  function renderSubs() {
    const list = $('sub-list');
    const subs = state.subscriptions || [];
    $('subs-empty').hidden = subs.length > 0;
    list.innerHTML = subs.map((sub) => {
      const info = sub.userInfo || {};
      const used = (info.uploadBytes || 0) + (info.downloadBytes || 0);
      const traffic = info.totalBytes ? `${formatBytes(used)} из ${formatBytes(info.totalBytes)}` : formatBytes(used) + ' использовано';
      const expire = info.expireUnix ? ' · до ' + new Date(info.expireUnix * 1000).toLocaleDateString('ru-RU') : '';
      return `
      <div class="row-item${sub.id === state.selectedSubscriptionId ? ' is-selected' : ''}" data-sub="${escapeHtml(sub.id)}">
        <span class="row-main">
          <span class="row-title">${escapeHtml(sub.name || 'Подписка')}</span>
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
    $('set-autorefresh').checked = !!settings.autoRefresh;
    $('set-autoconnect').checked = !!settings.autoConnect;
    $('set-blockads').checked = !!settings.blockAds;
    $('set-tray').checked = settings.minimizeToTray !== false;
    $('set-dns1').value = settings.dns1 || '';
    $('set-dns2').value = settings.dns2 || '';
    $('set-testurl').value = settings.testUrl || '';
    $('set-support').value = settings.supportUrl || '';
    $('set-update').value = settings.updateUrl || '';
  }

  function renderInfo() {
    const core = state.core || {};
    if ($('update-title').textContent === 'Проверка обновлений') {
      setText($('update-sub'), 'Установлена версия ' + (state.version || '—'));
    }
    $('info-version').textContent = state.version || '—';
    $('info-core').textContent = core.version || '—';
    $('info-core-state').textContent = core.downloading
      ? `Загрузка ядра… ${core.progress || 0}%`
      : core.installed ? 'Ядро Xray установлено' : 'Ядро загрузится при первом подключении';
    $('info-device').textContent = (device && device.deviceName) || '—';
    $('info-os').textContent = (device && device.osName) || 'Windows';
    $('info-id').textContent = (device && device.deviceId) || '—';
  }

  function updateTimer() {
    const connection = (state && state.connection) || {};
    $('power-timer').textContent = connection.connected ? formatUptime(connection.startedAt) : '00:00:00';
  }

  function updateWelcome() {
    const hasSubs = (state.subscriptions || []).length > 0;
    $('welcome').hidden = hasSubs;
    $('shell').hidden = !hasSubs;
  }

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
      $('welcome-url').value = '';
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

  async function connect(nodeId) {
    await guard(async () => {
      state = await api('/api/connect', { method: 'POST', body: { nodeId: nodeId || state.selectedNodeId || '', mode: '' } });
    });
  }

  async function disconnect() {
    await guard(async () => {
      state = await api('/api/disconnect', { method: 'POST', body: {} });
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
    });
  });

  const openPage = (page) => {
    const button = document.querySelector(`.nav-item[data-page="${page}"]`);
    if (button) button.click();
  };
  $('server-chip').addEventListener('click', () => openPage('servers'));

  $('power-btn').addEventListener('click', () => {
    if (!state) return;
    if (state.connection.connected) disconnect(); else connect('');
  });

  $('server-search').addEventListener('input', (event) => {
    search = event.target.value;
    renderServers();
  });

  $('server-list').addEventListener('click', (event) => {
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
  $('welcome-add').addEventListener('click', () => addSubscription($('welcome-url').value.trim()));
  $('welcome-url').addEventListener('keydown', (event) => {
    if (event.key === 'Enter') $('welcome-add').click();
  });

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
    if (deleteBtn) {
      guard(async () => {
        state = await api('/api/subscriptions/delete', { method: 'POST', body: { id: deleteBtn.dataset.subDelete } });
        toast('Подписка удалена');
      });
    }
  });

  $('settings-save').addEventListener('click', () => guard(async () => {
    state = await api('/api/settings', {
      method: 'POST',
      body: {
        connectionMode: $('set-mode').value,
        autoRefresh: $('set-autorefresh').checked,
        autoConnect: $('set-autoconnect').checked,
        // Раньше здесь жёстко стояло false, поэтому галочка блокировки
        // рекламы сбрасывалась при каждом сохранении настроек.
        blockAds: $('set-blockads').checked,
        minimizeToTray: $('set-tray').checked,
        dns1: $('set-dns1').value.trim(),
        dns2: $('set-dns2').value.trim(),
        testUrl: $('set-testurl').value.trim(),
        supportUrl: $('set-support').value.trim(),
        updateUrl: $('set-update').value.trim()
      }
    });
    toast('Настройки сохранены', 'ok');
  }));

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

  async function loadLog() {
    try {
      const data = await api('/api/log');
      $('log-body').textContent = (data && data.log) ? data.log : 'Журнал пуст.';
    } catch (error) {
      $('log-body').textContent = error.message;
    }
  }
  $('log-refresh').addEventListener('click', loadLog);
  renderEvents();

  /* ==================== запуск ==================== */

  api('/api/device').then((data) => { device = data; renderInfo(); }).catch(() => {});
  refresh();
  setInterval(refresh, 2500);
  timerHandle = setInterval(updateTimer, 1000);
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
