'use strict';
(() => {
  const root = document.getElementById('root');

  // ---------- тема: тёмная по умолчанию, выбор запоминается ----------
  const THEME_KEY = 'randdesk-theme';
  const getTheme = () => document.documentElement.dataset.theme || 'dark';
  function setTheme(t, animate) {
    const el = document.documentElement;
    if (animate) {
      el.classList.add('theme-anim');
      setTimeout(() => el.classList.remove('theme-anim'), 450);
    }
    el.dataset.theme = t;
    try { localStorage.setItem(THEME_KEY, t); } catch {}
    document.querySelectorAll('.theme-btn').forEach(updateThemeBtn);
  }
  try { setTheme(localStorage.getItem(THEME_KEY) === 'light' ? 'light' : 'dark'); } catch { setTheme('dark'); }
  const state = { user: null, hubUrls: [], cleanup: null };

  // ---------- DOM-утилиты ----------
  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (v == null || v === false) continue;
        if (k === 'class') el.className = v;
        else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
        else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
        else if (k === 'checked' || k === 'value' || k === 'disabled') el[k] = v;
        else el.setAttribute(k, v === true ? '' : v);
      }
    }
    for (const c of kids.flat(Infinity)) {
      if (c == null || c === false) continue;
      el.append(c instanceof Node ? c : String(c));
    }
    return el;
  }

  const ICONS = {
    devices: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
    run: '<path d="M4 17l6-5-6-5M12 19h8"/>',
    log: '<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
    access: '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/><path d="M9 12l2 2 4-4"/>',
    settings: '<path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6"/>',
    logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
    power: '<path d="M18.36 6.64a9 9 0 1 1-12.73 0M12 2v10"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/>',
    moon: '<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>',
    terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M13 15h4"/>',
  };
  function icon(name) {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    s.setAttribute('viewBox', '0 0 24 24');
    s.setAttribute('class', 'ic');
    s.innerHTML = ICONS[name] || ''; // только статические строки
    return s;
  }
  function logo() {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    s.setAttribute('viewBox', '0 0 32 32');
    s.setAttribute('class', 'logo');
    const id = 'lg' + Math.random().toString(36).slice(2, 8);
    s.innerHTML = `<defs><linearGradient id="${id}" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#5b93ff"/><stop offset=".55" stop-color="#8b6cff"/><stop offset="1" stop-color="#c06cf0"/></linearGradient></defs>`
      + `<rect width="32" height="32" rx="9" fill="url(#${id})"/><rect x="7" y="9" width="18" height="12" rx="2.5" fill="none" stroke="#fff" stroke-width="2.4"/><path d="M12 25h8" stroke="#fff" stroke-width="2.4" stroke-linecap="round"/><circle cx="24.5" cy="8.5" r="3.2" fill="#3fd07a" stroke="#fff" stroke-width="1.5"/>`;
    return s;
  }

  function updateThemeBtn(b) {
    const dark = getTheme() === 'dark';
    b.replaceChildren(icon(dark ? 'sun' : 'moon'));
    b.title = dark ? 'Светлая тема' : 'Тёмная тема';
    b.setAttribute('aria-label', b.title);
  }
  function themeButton() {
    const b = h('button', { class: 'btn ghost sm theme-btn', type: 'button', onclick: () => setTheme(getTheme() === 'dark' ? 'light' : 'dark', true) });
    updateThemeBtn(b);
    return b;
  }
  const themeFloat = () => h('div', { class: 'theme-float' }, themeButton());

  // ---------- форматирование ----------
  const fmtBytes = (b) => {
    if (b == null || isNaN(b)) return '—';
    const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
    let i = 0;
    while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
    return (i ? b.toFixed(b < 10 ? 1 : 0) : Math.round(b)) + ' ' + u[i];
  };
  const fmtRate = (b) => fmtBytes(b) + '/с';
  const fmtPct = (v) => (v == null || isNaN(v) ? '—' : v.toFixed(v < 10 ? 1 : 0) + '%');
  const pct = (a, b) => (b ? (a / b) * 100 : 0);
  const fmtUptime = (s) => {
    if (!s) return '—';
    const d = Math.floor(s / 86400), hh = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
    return d ? `${d} д ${hh} ч` : hh ? `${hh} ч ${m} мин` : `${m} мин`;
  };
  const fmtAgo = (iso) => {
    const t = new Date(iso).getTime();
    if (!t || t < 0) return 'никогда';
    const s = (Date.now() - t) / 1000;
    if (s < 60) return 'только что';
    if (s < 3600) return Math.floor(s / 60) + ' мин назад';
    if (s < 86400) return Math.floor(s / 3600) + ' ч назад';
    return new Date(t).toLocaleString('ru-RU');
  };
  const fmtTime = (iso) => new Date(iso).toLocaleString('ru-RU');
  const level = (p) => (p >= 90 ? 'bad' : p >= 75 ? 'warn' : '');
  const cssVar = (n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();
  const shortOS = (info) => (info && info.platform ? info.platform.replace(/^Microsoft\s+/i, '') : '');
  const primaryIP = (d) => {
    for (const n of (d.info && d.info.nics) || []) for (const ip of n.ips || []) if (!ip.startsWith('169.254.')) return ip;
    return d.last_ip || '';
  };
  const hasMac = (d) => ((d.info && d.info.nics) || []).some((n) => n.mac);
  const enc = encodeURIComponent;
  const rpc = (id, m) => `/api/devices/${enc(id)}/rpc/${m}`;

  // ---------- API ----------
  class ApiError extends Error { constructor(status, msg) { super(msg); this.status = status; } }
  async function api(method, path, body) {
    const opt = { method, headers: { 'X-Requested-With': 'randdesk' }, credentials: 'same-origin' };
    if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
    let r;
    try { r = await fetch(path, opt); } catch { throw new ApiError(0, 'Хаб недоступен'); }
    const txt = await r.text();
    let data = null;
    if (txt) { try { data = JSON.parse(txt); } catch { data = txt; } }
    if (!r.ok) {
      if ((r.status === 401 || (r.status === 503 && state.device)) && state.user && !path.startsWith('/api/login')) { state.user = null; boot(); }
      throw new ApiError(r.status, (data && data.error) || r.statusText);
    }
    return data;
  }

  function poll(fn, ms) {
    let stopped = false, t = null;
    const tick = async () => {
      if (stopped) return;
      try { await fn(); } catch (e) { if (e.status !== 401) console.warn(e); }
      if (!stopped) t = setTimeout(tick, ms);
    };
    tick();
    return () => { stopped = true; clearTimeout(t); };
  }

  function toast(msg, kind = '', ms = 4300) {
    const el = h('div', { class: 'toast ' + kind }, msg);
    document.getElementById('toasts').append(el);
    setTimeout(() => el.classList.add('out'), ms - 500);
    setTimeout(() => el.remove(), ms);
  }

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = h('textarea', { style: { position: 'fixed', opacity: '0' } });
      ta.value = text;
      document.body.append(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    toast('Скопировано', 'ok');
  }

  function modal(title, content, actions) {
    actions = actions || [{ label: 'Закрыть', value: null }];
    return new Promise((resolve) => {
      let closed = false;
      const close = (v) => {
        if (closed) return;
        closed = true;
        document.removeEventListener('keydown', onKey);
        wrap.classList.add('closing');
        setTimeout(() => wrap.remove(), 180);
        resolve(v);
      };
      const onKey = (e) => { if (e.key === 'Escape') close(null); };
      const submitA = actions.find((a) => a.submit);
      const box = h('form', {
        class: 'modal',
        onsubmit: (e) => { e.preventDefault(); if (submitA) close(submitA.getValue ? submitA.getValue() : submitA.value); },
      },
        h('div', { class: 'modal-head' }, h('h2', null, title),
          h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Закрыть', onclick: () => close(null) }, '×')),
        h('div', { class: 'modal-body' }, content),
        h('div', { class: 'modal-foot' }, actions.map((a) =>
          h('button', { class: 'btn ' + (a.kind || ''), type: a.submit ? 'submit' : 'button', onclick: a.submit ? null : () => close(a.value) }, a.label))));
      const wrap = h('div', { class: 'modal-wrap', onmousedown: (e) => { if (e.target === wrap) close(null); } }, box);
      document.body.append(wrap);
      document.addEventListener('keydown', onKey);
      const f = box.querySelector('.modal-body input, .modal-body select, .modal-body textarea') || box.querySelector('.modal-foot .btn.primary, .modal-foot .btn.danger');
      if (f) f.focus();
    });
  }
  const confirmDlg = (title, text, okLabel = 'Подтвердить', kind = 'primary') =>
    modal(title, h('p', null, text), [{ label: 'Отмена', value: false }, { label: okLabel, kind, submit: true, value: true }]);
  const promptDlg = (title, label, value = '') => {
    const inp = h('input', { type: 'text', value });
    return modal(title, h('label', { class: 'field' }, h('span', null, label), inp),
      [{ label: 'Отмена', value: null }, { label: 'Сохранить', kind: 'primary', submit: true, getValue: () => inp.value }]);
  };

  // ---------- вход / первичная настройка ----------
  async function boot() {
    if (state.cleanup) { try { state.cleanup(); } catch {} state.cleanup = null; }
    let st;
    try { st = await api('GET', '/api/state'); } catch (e) {
      root.replaceChildren(h('div', { class: 'boot' }, 'Хаб недоступен: ' + e.message));
      setTimeout(boot, 3000);
      return;
    }
    state.hubUrls = st.hub_urls || [];
    state.device = null;
    if (st.node_setup) return renderNodeSetup(st);
    if (st.node_member && st.searching) return renderMemberWait();
    if (st.as_device) { // обычный ПК: доступны только устройства, разрешённые администратором
      state.device = st.as_device;
      state.user = st.as_device;
      renderShell();
      route();
      return;
    }
    if (st.setup_needed) return renderAuth('setup', st);
    if (!st.user) return renderAuth('login', st);
    state.user = st.user;
    renderShell();
    route();
  }

  // ---------- первая настройка ПК («Настроить ПК») ----------
  function renderNodeSetup(st) {
    const err = h('div', { class: 'form-err' });
    let role = 'member';
    const user = h('input', { type: 'text', autocomplete: 'username', placeholder: 'Логин', required: true });
    const pass = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'Пароль (от 8 символов)', required: true });
    const pass2 = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'Повторите пароль' });
    const hint = h('p', { class: 'muted small' });
    const roleCard = (value, title, text) => {
      const rb = h('input', { type: 'radio', name: 'role', value, checked: value === role });
      rb.addEventListener('change', () => { role = value; upd(); });
      return h('label', { class: 'role-card' }, rb, h('span', null, h('b', null, title), h('span', { class: 'muted small' }, text)));
    };
    const upd = () => {
      pass2.style.display = role === 'admin' ? '' : 'none';
      pass2.required = role === 'admin';
      hint.textContent = role === 'admin'
        ? 'Придумайте логин и пароль. На остальных ПК введите те же — и они сами подключатся к этому ПК.'
        : 'Введите тот же логин и пароль, что на главном ПК. Этот ПК сам найдёт главный в сети.';
    };
    const btn = h('button', { class: 'btn primary wide', type: 'submit' }, 'Настроить ПК');
    const form = h('form', {
      class: 'auth-card setup-card',
      onsubmit: async (e) => {
        e.preventDefault();
        err.textContent = '';
        if (role === 'admin' && pass.value !== pass2.value) { err.textContent = 'Пароли не совпадают'; return; }
        btn.disabled = true; btn.textContent = 'Настраиваю…';
        try {
          await api('POST', '/api/node/setup', { role, user: user.value, password: pass.value });
          root.replaceChildren(h('div', { class: 'boot' }, 'Готово, запускаю RANDSEC…'));
          const t0 = Date.now();
          const wait = async () => {
            try { const s = await api('GET', '/api/state'); if (!s.node_setup) return boot(); } catch {}
            if (Date.now() - t0 < 30000) setTimeout(wait, 700); else boot();
          };
          setTimeout(wait, 800);
        } catch (ex) { err.textContent = ex.message; btn.disabled = false; btn.textContent = 'Настроить ПК'; }
      },
    },
      h('div', { class: 'brand big' }, logo(), h('span', null, 'RANDSEC')),
      h('h2', null, 'Настройка ПК «' + (st.host || 'этот ПК') + '»'),
      h('div', { class: 'role-list' },
        roleCard('admin', 'Главный ПК (администратор)', 'Отсюда вы управляете всеми ПК и раздаёте разрешения.'),
        roleCard('member', 'Обычный ПК', 'Подключается к главному ПК. Управлять другими ПК — только с разрешения администратора.')),
      user, pass, pass2, hint, err, btn);
    upd();
    root.replaceChildren(h('div', { class: 'auth' }, themeFloat(), form));
    user.focus();
  }

  // ---------- обычный ПК ищет главный ----------
  function renderMemberWait() {
    const status = h('p', { class: 'muted' }, 'Поиск главного ПК…');
    const err = h('div', { class: 'form-err' });
    const addr = h('input', { type: 'text', placeholder: 'например 203.0.113.5 или myhome.duckdns.org' });
    const btn = h('button', { class: 'btn sm', type: 'submit' }, 'Подключиться');
    const manual = h('form', { class: 'manual', onsubmit: async (e) => {
      e.preventDefault(); btn.disabled = true; err.textContent = '';
      try { await api('POST', '/api/node/connect', { address: addr.value }); toast('Подключено', 'ok'); } catch (ex) { err.textContent = ex.message; } finally { btn.disabled = false; }
    } }, h('p', { class: 'muted small' }, 'Главный ПК в другой сети (через интернет)? Введите его внешний адрес — он есть в настройках главного ПК:'),
      h('div', { class: 'row' }, addr, btn));
    root.replaceChildren(h('div', { class: 'auth' }, themeFloat(), h('div', { class: 'auth-card' },
      h('div', { class: 'brand big' }, logo(), h('span', null, 'RANDSEC')),
      h('div', { class: 'spinner' }), status, err,
      h('p', { class: 'muted small' }, 'Убедитесь, что главный ПК включён и на нём RANDSEC настроен с тем же логином и паролем. ПК в одной сети (или в Radmin VPN) найдут друг друга сами.'),
      manual,
      h('button', { class: 'btn sm ghost', style: { marginTop: '10px' }, onclick: async () => {
        if (!await confirmDlg('Изменить логин и пароль?', 'Этот ПК вернётся к первой настройке, и вы сможете ввести логин и пароль заново.', 'Изменить')) return;
        try {
          await api('POST', '/api/node/reset');
          root.replaceChildren(h('div', { class: 'boot' }, 'Загрузка…'));
          setTimeout(boot, 1200);
        } catch (e) { toast(e.message, 'bad'); }
      } }, 'Изменить логин и пароль'))));
    let timer = null;
    const tick = async () => {
      try {
        const s = await api('GET', '/api/node/status');
        const zt = s.zt || {};
        status.textContent = s.online ? 'Подключено к главному ПК ' + (s.admin || '')
          : zt.step ? 'Поиск главного ПК… ' + zt.step
          : zt.status === 'OK' ? 'Поиск главного ПК… (сеть через интернет подключена)'
          : (s.status || 'Поиск главного ПК…');
        if (s.error && !s.online) err.textContent = s.error === 'на главном ПК другой логин или пароль' ? 'Главный ПК найден, но логин или пароль не совпадают.' : s.error;
        if (s.online) return boot();
      } catch {}
      timer = setTimeout(tick, 2000);
    };
    tick();
    state.cleanup = () => clearTimeout(timer);
  }

  function renderAuth(mode, st) {
    const setup = mode === 'setup';
    const err = h('div', { class: 'form-err' });
    const user = h('input', { type: 'text', autocomplete: 'username', placeholder: 'Имя пользователя', required: true });
    const pass = h('input', { type: 'password', autocomplete: setup ? 'new-password' : 'current-password', placeholder: 'Пароль', required: true });
    const pass2 = setup ? h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'Повторите пароль', required: true }) : null;
    const btn = h('button', { class: 'btn primary wide', type: 'submit' }, setup ? 'Создать администратора' : 'Войти');
    const form = h('form', {
      class: 'auth-card',
      onsubmit: async (e) => {
        e.preventDefault();
        err.textContent = '';
        if (setup && pass.value !== pass2.value) { err.textContent = 'Пароли не совпадают'; return; }
        btn.disabled = true;
        try {
          const r = await api('POST', setup ? '/api/setup' : '/api/login', { user: user.value, password: pass.value });
          if (r && r.totp_required) return renderTOTP(r.ticket);
          boot();
        } catch (ex) { err.textContent = ex.message; } finally { btn.disabled = false; }
      },
    },
      h('div', { class: 'brand big' }, logo(), h('span', null, 'RANDSEC')),
      h('p', { class: 'muted' }, setup ? 'Первый запуск: создайте учётную запись администратора.' : 'Вход в панель управления'),
      setup && !st.can_setup ? h('div', { class: 'form-err' }, 'Первичная настройка доступна только на ПК с хабом — откройте там http://localhost:8420') : null,
      user, pass, pass2, err, btn);
    root.replaceChildren(h('div', { class: 'auth' }, themeFloat(), form));
    user.focus();
  }

  // второй шаг входа: код из приложения-аутентификатора или резервный код
  function renderTOTP(ticket) {
    const err = h('div', { class: 'form-err' });
    const code = h('input', { type: 'text', autocomplete: 'one-time-code', inputmode: 'numeric', placeholder: '123 456', required: true, class: 'otp', maxlength: '12' });
    const btn = h('button', { class: 'btn primary wide', type: 'submit' }, 'Подтвердить');
    const form = h('form', {
      class: 'auth-card',
      onsubmit: async (e) => {
        e.preventDefault();
        err.textContent = '';
        btn.disabled = true;
        try {
          await api('POST', '/api/login/totp', { ticket, code: code.value });
          boot();
        } catch (ex) {
          err.textContent = ex.message;
          code.select();
          if (ex.status === 401 && /истёк/.test(ex.message)) setTimeout(boot, 1500);
        } finally { btn.disabled = false; }
      },
    },
      h('div', { class: 'brand big' }, logo(), h('span', null, 'RANDSEC')),
      h('p', { class: 'muted' }, 'Введите 6-значный код из приложения-аутентификатора.'),
      code, err, btn,
      h('p', { class: 'muted small' }, 'Нет доступа к телефону? Введите один из резервных кодов (вида ABCD-EFGH).'),
      h('button', { class: 'btn ghost sm', type: 'button', onclick: boot }, '← Назад'));
    root.replaceChildren(h('div', { class: 'auth' }, themeFloat(), form));
    code.focus();
  }

  async function logout() {
    try { await api('POST', '/api/logout'); } catch {}
    state.user = null;
    location.hash = '#/';
    boot();
  }

  // ---------- каркас и маршрутизация ----------
  const NAV = [['#/', 'Устройства', 'devices'], ['#/access', 'Доступ', 'access'], ['#/exec', 'Массовое выполнение', 'run'], ['#/audit', 'Журнал', 'log'], ['#/settings', 'Настройки', 'settings']];
  const NAV_DEVICE = [['#/', 'Доступные ПК', 'devices']];

  function renderShell() {
    const side = h('aside', { class: 'side' },
      h('div', { class: 'brand' }, logo(), h('span', null, 'RANDSEC')),
      h('nav', null, (state.device ? NAV_DEVICE : NAV).map(([href, label, ic]) => h('a', { href, 'data-nav': href, title: label }, icon(ic), h('span', null, label)))),
      state.device
        ? h('div', { class: 'side-foot' }, h('span', { class: 'muted small ell', title: 'Управление — с разрешения администратора' }, 'Этот ПК: ' + state.device), themeButton())
        : h('div', { class: 'side-foot' }, h('span', { class: 'muted small ell' }, state.user), themeButton(),
          h('button', { class: 'btn ghost sm', onclick: logout, title: 'Выйти' }, icon('logout'), 'Выйти')));
    root.replaceChildren(h('div', { class: 'app' }, side, h('main', { class: 'main', id: 'view' })));
  }

  function route() {
    if (!state.user) return;
    if (state.cleanup) { try { state.cleanup(); } catch {} state.cleanup = null; }
    const view = document.getElementById('view');
    if (!view) return;
    const path = location.hash.slice(1) || '/';
    const parts = path.split('/').filter(Boolean);
    document.querySelectorAll('[data-nav]').forEach((a) => {
      const n = a.getAttribute('data-nav').slice(2);
      a.classList.toggle('active', n === '' ? !parts[0] || parts[0] === 'device' : parts[0] === n);
    });
    view.replaceChildren();
    view.classList.remove('page-enter');
    void view.offsetWidth; // перезапуск анимации появления страницы
    view.classList.add('page-enter');
    let c;
    if (parts[0] === 'device' && parts[1]) c = pageDevice(view, decodeURIComponent(parts[1]), parts[2] || 'overview');
    else if (parts[0] === 'exec') c = pageExec(view);
    else if (parts[0] === 'audit') c = pageAudit(view);
    else if (state.device) c = pageDevices(view);
    else if (parts[0] === 'settings') c = pageSettings(view);
    else if (parts[0] === 'access') c = pageAccess(view);
    else c = pageDevices(view);
    state.cleanup = c || null;
    window.scrollTo(0, 0);
  }
  window.addEventListener('hashchange', route);

  // ---------- общие компоненты ----------
  const tile = (k, v, cls = '') => h('div', { class: 'tile ' + cls }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, v));
  const meter = (label, p, text) => h('div', { class: 'meter' },
    h('div', { class: 'meter-row' }, h('span', null, label), h('span', { class: 'muted' }, text)),
    h('div', { class: 'bar ' + level(p) }, h('i', { style: { width: Math.max(0, Math.min(100, p)).toFixed(1) + '%' } })));
  const cmdBlock = (text) => {
    const code = h('code', { class: 'cmd' }, text);
    return { el: h('div', { class: 'cmd-wrap' }, code, h('button', { class: 'btn sm', type: 'button', onclick: () => copyText(code.textContent) }, 'Копировать')), code };
  };

  async function wake(d) {
    try {
      const r = await api('POST', `/api/devices/${enc(d.id)}/wake`);
      const lan = r.lan_relays || [];
      if (lan.length) {
        toast(`Magic-пакет отправлен на ${r.macs.join(', ')} через ${lan.join(', ')} (в одной сети с ${d.name}). ПК должен включиться в течение минуты.`, 'ok');
      } else {
        const nets = [...new Set((r.target_ips || []).map(ip => ip.replace(/\.\d+$/, '.x')))].join(', ');
        toast(`Magic-пакет отправлен на ${r.macs.join(', ')}, но в сети ${d.name}${nets ? ' (' + nets + ')' : ''} сейчас нет включённого ПК с RANDSEC. ` +
          'Пакет послан напрямую на последний адрес ПК — дойдёт, только если маршрутизатор его пропустит. ' +
          'Надёжно: держите включённым любой другой ПК с RANDSEC в той же сети.', 'warn', 12000);
      }
    } catch (e) { toast(e.message, 'bad'); }
  }

  const KIND = { lan: 'локальная сеть', vpn: 'VPN', public: 'интернет' };

  async function showAddDevice() {
    let k;
    try { k = await api('GET', '/api/enroll-key'); } catch (e) { toast(e.message, 'bad'); return; }
    if (k.node) {
      const adv = h('button', { class: 'btn sm ghost', type: 'button', onclick: () => { document.querySelector('.modal-wrap').remove(); showAddDeviceKey(k); } }, 'Другой способ — по ключу привязки');
      return modal('Добавить ПК', [
        h('ol', { class: 'steps' },
          h('li', null, 'Скопируйте randsec.exe на другой ПК (или скачайте со страницы релизов).'),
          h('li', null, 'Запустите его двойным щелчком и разрешите установку.'),
          h('li', null, 'В открывшемся окне выберите «Обычный ПК», введите тот же логин и пароль, что здесь, и нажмите «Настроить ПК».')),
        h('p', { class: 'muted small' }, 'ПК в одной сети (или в Radmin VPN) найдут друг друга сами. Если другой ПК в другой сети, укажите на нём адрес этого ПК из «Настройки → Доступ из интернета».'),
        adv]);
    }
    return showAddDeviceKey(k);
  }

  async function showAddDeviceKey(k) {
    const addrs = (k.addrs && k.addrs.length) ? k.addrs : [{ url: location.origin, kind: 'lan' }];
    const inst = cmdBlock(''), cons = cmdBlock('');
    const warn = h('div', { class: 'form-err' });
    const checks = addrs.map((a) => {
      const cb = h('input', { type: 'checkbox', checked: true });
      return { a, cb, el: h('label', { class: 'pick' }, cb, h('span', { class: 'mono small' }, a.url), h('span', { class: 'badge ' + a.kind }, KIND[a.kind] || a.kind)) };
    });
    const upd = () => {
      const list = checks.filter((c) => c.cb.checked).map((c) => c.a.url);
      const hub = list.join(',') || location.origin;
      const fp = k.fingerprint ? ` -fp ${k.fingerprint}` : '';
      inst.code.textContent = `.\\randsec.exe agent install -hub ${hub} -key ${k.key}${fp}`;
      cons.code.textContent = `.\\randsec.exe agent -hub ${hub} -key ${k.key}${fp}`;
      warn.textContent = list.length ? '' : 'Выберите хотя бы один адрес.';
    };
    checks.forEach((c) => c.cb.addEventListener('change', upd));
    upd();
    const hasPublic = addrs.some((a) => a.kind === 'public');
    modal('Добавить ПК', [
      h('ol', { class: 'steps' },
        h('li', null, 'Скопируйте randsec.exe на нужный ПК.'),
        h('li', null, 'Отметьте адреса хаба. Агент пробует их по порядку: дома подключится по локальной сети, вне дома — через интернет.',
          h('div', { class: 'pick-list col' }, checks.map((c) => c.el)), warn,
          hasPublic ? null : h('p', { class: 'muted small' }, 'Внешнего адреса пока нет. Чтобы ПК подключался не только из локальной сети, включите «Доступ из интернета» в настройках.')),
        h('li', null, 'В PowerShell от администратора установите агента службой (запускается вместе с Windows):', inst.el),
        h('li', null, 'Или для проверки запустите агента в консоли, без установки:', cons.el)),
      h('p', { class: 'muted small' }, 'Параметр -fp — отпечаток сертификата хаба: агент не подключится к подменённому хабу. Новые адреса хаба (например, после смены внешнего IP) агент получит сам. Ключ привязки можно сменить в настройках — уже привязанные ПК продолжат работать.'),
    ]);
  }

  // ---------- страница: устройства ----------
  function pageDevices(view) {
    const tiles = h('div', { class: 'tiles' });
    const grid = h('div', { class: 'dev-grid' });
    view.append(
      h('div', { class: 'page-head' }, h('h1', null, state.device ? 'Доступные ПК' : 'Устройства'),
        state.device ? null : h('div', { class: 'actions' }, h('button', { class: 'btn primary', onclick: showAddDevice }, '+ Добавить ПК'))),
      state.device ? h('p', { class: 'muted', style: { marginTop: '-8px' } }, 'ПК, которыми администратор разрешил управлять с этого ПК.') : '',
      tiles, grid);
    let empty = null, firstGrid = true;
    tiles.classList.add('stagger');
    setTimeout(() => tiles.classList.remove('stagger'), 1000);
    return poll(async () => {
      const devs = await api('GET', '/api/devices');
      const online = devs.filter((d) => d.online && d.stats);
      const avgCpu = online.length ? online.reduce((a, d) => a + d.stats.cpu, 0) / online.length : null;
      const mu = online.reduce((a, d) => a + d.stats.mem_used, 0), mt = online.reduce((a, d) => a + d.stats.mem_total, 0);
      tiles.replaceChildren(
        tile('Всего ПК', devs.length),
        tile('В сети', devs.filter((d) => d.online).length, devs.some((d) => d.online) ? 'ok' : ''),
        tile('Средняя загрузка CPU', avgCpu == null ? '—' : fmtPct(avgCpu)),
        tile('Память (ПК в сети)', mt ? `${fmtBytes(mu)} / ${fmtBytes(mt)}` : '—'));
      if (!devs.length) {
        if (!empty) {
          empty = state.device
            ? h('div', { class: 'empty' }, h('h2', null, 'Пока нет доступных ПК'),
              h('p', null, 'Этот ПК подключён к главному. Управлять другими ПК можно после того, как администратор разрешит это на главном ПК (страница «Доступ»).'))
            : h('div', { class: 'empty' }, h('h2', null, 'Пока нет ни одного ПК'),
              h('p', null, 'Запустите randsec.exe на другом ПК, выберите «Обычный ПК» и введите тот же логин и пароль.'),
              h('button', { class: 'btn primary', onclick: showAddDevice }, 'Как добавить ПК'));
          grid.replaceChildren(empty);
        }
        return;
      }
      empty = null;
      const cards = devs.map(deviceCard);
      if (firstGrid) { // карточки появляются по очереди только при открытии страницы
        cards.forEach((c, i) => c.style.setProperty('--i', i));
        grid.classList.add('stagger');
        setTimeout(() => grid.classList.remove('stagger'), 1200);
        firstGrid = false;
      }
      grid.replaceChildren(...cards);
    }, 3000);
  }

  // тип связи с ПК: прямая локальная сеть (быстро) или через интернет
  const ROUTES = {
    lan: ['локальная сеть', 'lan', 'Прямое подключение по локальной сети — максимальная скорость'],
    vpn: ['VPN', 'vpn', 'Подключение через VPN'],
    zt: ['через интернет', 'zt', 'Подключение через интернет (ZeroTier)'],
    internet: ['через интернет', 'zt', 'Подключение через интернет'],
  };
  const routeBadge = (d) => {
    const r = d.online && ROUTES[d.route];
    return r ? h('span', { class: 'badge route ' + r[1], title: r[2] }, r[0]) : null;
  };

  // значок «ввод отключён» на карточке ПК
  function lockBadge(d) {
    const l = d.lock || {};
    const off = [l.keyboard && 'клавиатура', l.mouse && 'мышь', l.gamepad && 'геймпад'].filter(Boolean);
    return off.length ? h('span', { class: 'pill warn', title: 'Отключено администратором: ' + off.join(', ') }, '⛔ ' + off.join(', ')) : null;
  }

  function deviceCard(d) {
    const st = d.stats;
    const ip = primaryIP(d);
    const disks = (st && st.disks) || [];
    const sysDisk = disks.find((x) => /^C:/i.test(x.mount)) || disks[0];
    const href = '#/device/' + enc(d.id);
    return h('div', { class: 'dev-card' + (d.online ? '' : ' offline') },
      h('div', { class: 'dev-top' }, h('span', { class: 'dot ' + (d.online ? 'on' : 'off') }),
        h('a', { class: 'dev-name ell', href }, d.name), d.self ? h('span', { class: 'pill' }, 'главный') : null,
        lockBadge(d), routeBadge(d), h('span', { class: 'muted small right ell' }, shortOS(d.info))),
      h('div', { class: 'muted small ell' }, [d.info.hostname, ip].filter(Boolean).join(' · ') || '—'),
      d.online && st
        ? h('div', { class: 'meters' },
          meter('CPU', st.cpu, fmtPct(st.cpu)),
          meter('Память', pct(st.mem_used, st.mem_total), `${fmtBytes(st.mem_used)} / ${fmtBytes(st.mem_total)}`),
          sysDisk ? meter('Диск ' + sysDisk.mount.replace(/\\$/, ''), pct(sysDisk.used, sysDisk.total), `${fmtBytes(sysDisk.used)} / ${fmtBytes(sysDisk.total)}`) : null,
          h('div', { class: 'dev-net muted small' }, h('span', null, `↓ ${fmtRate(st.net_rx)}`), h('span', null, `↑ ${fmtRate(st.net_tx)}`),
            h('span', { class: 'right' }, 'аптайм ' + fmtUptime(st.uptime))))
        : h('div', { class: 'dev-offline muted small' }, d.online ? 'Получение данных…' : 'Не в сети · последний раз ' + fmtAgo(d.last_seen)),
      h('div', { class: 'dev-actions' },
        h('a', { class: 'btn sm', href }, 'Открыть'),
        d.online ? h('a', { class: 'btn sm primary', href: href + '/desktop' }, icon('devices'), 'Рабочий стол') : null,
        d.online ? h('a', { class: 'btn sm', href: href + '/terminal' }, icon('terminal'), 'Консоль') : null,
        !d.online && hasMac(d) ? h('button', { class: 'btn sm', onclick: () => wake(d) }, icon('power'), 'Включить') : null));
  }

  // ---------- страница: устройство ----------
  const TABS = [['overview', 'Обзор'], ['desktop', 'Рабочий стол'], ['terminal', 'Консоль'], ['processes', 'Процессы'], ['services', 'Службы'], ['run', 'Выполнить']];

  function pageDevice(view, id, tab) {
    if (!TABS.some((t) => t[0] === tab)) tab = 'overview';
    const head = h('div', { class: 'page-head' });
    const sub = h('div', { class: 'muted small', style: { margin: '-10px 0 16px' } });
    const body = h('div');
    view.append(h('a', { href: '#/', class: 'back small' }, '← Все устройства'), head, sub,
      h('div', { class: 'tabs' }, TABS.map(([k, l]) => h('a', { href: `#/device/${enc(id)}/${k}`, class: k === tab ? 'active' : '' }, l))),
      body);
    let dev = null, tabCleanup = null, started = false, lastKey = '';

    const power = async (action, label) => {
      if (!await confirmDlg(label + '?', `${label}: «${dev.name}». Несохранённые данные на этом ПК могут быть потеряны.`, label, 'danger')) return;
      try { await api('POST', rpc(id, 'power'), { action }); toast(`Команда «${label}» отправлена`, 'ok'); } catch (e) { toast(e.message, 'bad'); }
    };
    const rename = async () => {
      const n = await promptDlg('Переименовать ПК', 'Название', dev.name);
      if (!n || !n.trim()) return;
      try { await api('POST', `/api/devices/${enc(id)}/rename`, { name: n.trim() }); dev.name = n.trim(); lastKey = ''; renderHead(); } catch (e) { toast(e.message, 'bad'); }
    };
    const remove = async () => {
      if (!await confirmDlg('Удалить ПК?', `«${dev.name}» будет отвязан от хаба. Чтобы вернуть его, агента нужно привязать заново.`, 'Удалить', 'danger')) return;
      try { await api('DELETE', `/api/devices/${enc(id)}`); toast('ПК удалён', 'ok'); location.hash = '#/'; } catch (e) { toast(e.message, 'bad'); }
    };

    function renderHead() {
      const on = dev.online;
      const key = [dev.name, on, dev.route].join('|');
      if (key !== lastKey) {
        lastKey = key;
        head.replaceChildren(
          h('div', { class: 'dev-title' }, h('span', { class: 'dot ' + (on ? 'on' : 'off') }), h('h1', null, dev.name),
            state.device ? null : h('button', { class: 'icon-btn', title: 'Переименовать', onclick: rename }, '✎'),
            dev.self ? h('span', { class: 'pill' }, 'главный ПК') : null, routeBadge(dev),
            h('span', { class: 'pill ' + (on ? 'ok' : '') }, on ? 'в сети' : 'не в сети')),
          h('div', { class: 'actions' },
            on ? [
              h('button', { class: 'btn sm', onclick: () => power('reboot', 'Перезагрузить') }, 'Перезагрузить'),
              h('button', { class: 'btn sm', onclick: () => power('shutdown', 'Выключить') }, icon('power'), 'Выключить'),
              h('button', { class: 'btn sm', onclick: () => power('sleep', 'Перевести в сон') }, 'Сон'),
              h('button', { class: 'btn sm ghost', onclick: async () => { try { await api('POST', rpc(id, 'power'), { action: 'cancel' }); toast('Выключение отменено', 'ok'); } catch (e) { toast(e.message, 'bad'); } } }, 'Отменить выкл.'),
            ] : [
              hasMac(dev) ? h('button', { class: 'btn sm primary', onclick: () => wake(dev) }, icon('power'), 'Включить (WoL)') : null,
            ],
            state.device || dev.self ? null : h('button', { class: 'btn sm danger-ghost', onclick: remove }, 'Удалить')));
      }
      sub.textContent = [shortOS(dev.info), dev.info.hostname, primaryIP(dev), on ? null : 'последний раз в сети ' + fmtAgo(dev.last_seen)].filter(Boolean).join(' · ');
    }

    const stop = poll(async () => {
      try { dev = await api('GET', `/api/devices/${enc(id)}`); } catch (e) {
        if (e.status === 404) { head.replaceChildren(h('h1', null, 'Устройство не найдено')); stopNow(); return; }
        throw e;
      }
      renderHead();
      if (!started) {
        started = true;
        const fn = { overview: tabOverview, desktop: tabDesktop, terminal: tabTerminal, processes: tabProcesses, services: tabServices, run: tabRun }[tab];
        tabCleanup = fn(body, dev, () => dev);
      }
    }, 5000);
    let stopped = false;
    const stopNow = () => { if (!stopped) { stopped = true; stop(); } };
    return () => { stopNow(); if (tabCleanup) tabCleanup(); };
  }

  const offlineNote = (dev) => h('div', { class: 'empty' }, h('h2', null, 'ПК не в сети'),
    h('p', null, 'Эта функция доступна, когда агент подключён к хабу.'),
    hasMac(dev) ? h('button', { class: 'btn primary', onclick: () => wake(dev) }, icon('power'), 'Включить по сети (WoL)') : null);

  // --- обзор ---
  function chartBox(title, legend) {
    const val = h('span', { class: 'chart-val' });
    const cv = h('canvas');
    const el = h('div', { class: 'card' },
      h('div', { class: 'chart-head' }, h('h3', null, title), val),
      h('div', { class: 'chart-area' }, cv),
      legend ? h('div', { class: 'legend' }, legend.map(([c, l]) => h('span', null, h('i', { style: { background: cssVar(c) } }), l))) : null);
    return { el, draw(series, opts, label) { drawChart(cv, series, opts || {}); val.textContent = label; } };
  }

  function drawChart(cv, series, { max, win = 10 * 60e3, fmt } = {}) {
    const dpr = window.devicePixelRatio || 1;
    const w = cv.clientWidth, H = cv.clientHeight;
    if (!w || !H) return;
    if (cv.width !== Math.round(w * dpr) || cv.height !== Math.round(H * dpr)) { cv.width = Math.round(w * dpr); cv.height = Math.round(H * dpr); }
    const ctx = cv.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, H);
    let tMax = 0;
    for (const s of series) if (s.data.length) tMax = Math.max(tMax, s.data[s.data.length - 1][0]);
    const tMin = tMax - win;
    let yMax = max;
    if (!yMax) {
      yMax = 1024;
      for (const s of series) for (const [t, v] of s.data) if (t >= tMin && v > yMax) yMax = v;
      yMax *= 1.2;
    }
    ctx.strokeStyle = cssVar('--grid');
    ctx.lineWidth = 1;
    ctx.fillStyle = cssVar('--muted');
    ctx.font = '11px "Segoe UI", system-ui, sans-serif';
    for (let i = 0; i < 4; i++) {
      const y = Math.round((H - 4) * i / 4) + 0.5;
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(w, y); ctx.stroke();
    }
    ctx.fillText(fmt ? fmt(yMax) : Math.round(yMax) + '%', 4, 12);
    if (!tMax) return;
    for (const s of series) {
      const pts = s.data.filter(([t]) => t >= tMin);
      if (pts.length < 2) continue;
      const X = (t) => ((t - tMin) / win) * w;
      const Y = (v) => H - 2 - (Math.min(v, yMax) / yMax) * (H - 8);
      const color = cssVar(s.color);
      // плавная линия (сглаживание через середины отрезков) и градиентная заливка под ней
      const path = new Path2D();
      pts.forEach(([t, v], i) => {
        const x = X(t), y = Y(v);
        if (!i) { path.moveTo(x, y); return; }
        const [pt, pv] = pts[i - 1];
        const px = X(pt), py = Y(pv);
        path.quadraticCurveTo(px, py, (px + x) / 2, (py + y) / 2);
        if (i === pts.length - 1) path.lineTo(x, y);
      });
      const area = new Path2D(path);
      area.lineTo(X(pts[pts.length - 1][0]), H); area.lineTo(X(pts[0][0]), H); area.closePath();
      const grad = ctx.createLinearGradient(0, 0, 0, H);
      grad.addColorStop(0, color + '55'); grad.addColorStop(1, color + '00');
      ctx.fillStyle = grad; ctx.fill(area);
      ctx.strokeStyle = color; ctx.lineWidth = 2; ctx.lineJoin = 'round'; ctx.lineCap = 'round';
      ctx.shadowColor = color + '88'; ctx.shadowBlur = 8; ctx.stroke(path); ctx.shadowBlur = 0;
    }
  }

  // переключатель «включено/отключено»
  function toggle(label, hint, on, onChange, disabled) {
    const cb = h('input', { type: 'checkbox', checked: on, disabled: !!disabled });
    cb.addEventListener('change', async () => {
      cb.disabled = true;
      try { await onChange(cb.checked); } catch (e) { cb.checked = !cb.checked; toast(e.message, 'bad'); } finally { cb.disabled = !!disabled; }
    });
    return h('label', { class: 'switch-row' + (disabled ? ' disabled' : '') }, cb, h('span', { class: 'sw' }),
      h('span', null, h('b', null, label), hint ? h('div', { class: 'muted small' }, hint) : null));
  }

  // карточка администратора: устройства ввода и общий буфер обмена
  function inputCard(d) {
    const lock = { ...d.lock };
    const send = async () => { await api('POST', `/api/devices/${enc(d.id)}/input`, lock); };
    const set = (k, label) => async (on) => {
      lock[k] = !on;
      try { await send(); toast(`${label}: ${on ? 'включена' : 'отключена'}`, 'ok'); } catch (e) { lock[k] = on; throw e; }
    };
    const selfNote = d.self ? 'На главном ПК клавиатуру и мышь отключить нельзя.' : 'Удалённое управление через RANDSEC продолжит работать.';
    return h('div', { class: 'card input-card' },
      h('h3', null, 'Устройства ввода и буфер обмена'),
      h('div', { class: 'switches' },
        toggle('Клавиатура', selfNote, !lock.keyboard, set('keyboard', 'Клавиатура'), d.self),
        toggle('Мышь', selfNote, !lock.mouse, set('mouse', 'Мышь'), d.self),
        toggle('Геймпад', 'Игровые контроллеры (Xbox, DualSense и др.) отключаются в диспетчере устройств.', !lock.gamepad, async (on) => {
          lock.gamepad = !on;
          try { await send(); toast(`Геймпад: ${on ? 'включён' : 'отключён'}`, 'ok'); } catch (e) { lock.gamepad = on; throw e; }
        }),
        toggle('Общий буфер обмена', 'Скопированный текст появляется на других ПК. Включается для всех на странице «Доступ».', d.clip,
          async (on) => { await api('POST', `/api/devices/${enc(d.id)}/clipboard`, { enabled: on }); toast(on ? 'ПК участвует в общем буфере' : 'ПК исключён из общего буфера', 'ok'); })));
  }

  function tabOverview(body, dev0) {
    const id = dev0.id;
    const info = h('div', { class: 'info-grid' });
    const cpuC = chartBox('Загрузка CPU · 10 мин');
    const memC = chartBox('Память · 10 мин');
    const netC = chartBox('Сеть · 10 мин', [['--ok', 'приём'], ['--warn', 'передача']]);
    const disks = h('div');
    const nics = h('div', { class: 'kv' });
    body.append(info, state.device ? '' : inputCard(dev0), h('div', { class: 'charts' }, cpuC.el, memC.el, netC.el),
      h('div', { class: 'two-col' }, h('div', { class: 'card' }, h('h3', null, 'Диски'), disks), h('div', { class: 'card' }, h('h3', null, 'Сетевые адаптеры'), nics)));
    let points = [], since = 0;
    const cell = (k, v) => h('div', null, h('div', { class: 'k' }, k), h('div', { class: 'v', title: String(v) }, v));

    function render(d) {
      const i = d.info, st = d.stats;
      info.replaceChildren(
        cell('Операционная система', [shortOS(i), i.platform_version].filter(Boolean).join(' ') || '—'),
        cell('Процессор', i.cpu_model || '—'),
        cell('Ядра / потоки', i.cpu_cores ? `${i.cpu_cores} / ${i.cpu_threads}` : '—'),
        cell('Оперативная память', fmtBytes(i.mem_total)),
        cell('Время работы', st ? fmtUptime(st.uptime) : '—'),
        cell('Процессов', st ? st.procs : '—'),
        cell('Агент', `v${i.agent_version || '?'}${i.agent_user ? ' · ' + i.agent_user : ''}`),
        cell('Последний IP (для хаба)', d.last_ip || '—'));
      disks.replaceChildren(...(st && st.disks && st.disks.length ? st.disks.map((x) => h('div', { class: 'disk' },
        meter(`${x.mount} ${x.fs ? '(' + x.fs + ')' : ''}`, pct(x.used, x.total), `${fmtBytes(x.used)} из ${fmtBytes(x.total)} · свободно ${fmtBytes(x.total - x.used)}`)))
        : [h('div', { class: 'muted' }, d.online ? 'Нет данных' : 'ПК не в сети')]));
      nics.replaceChildren(...(i.nics || []).flatMap((n) => [h('span', { class: 'k ell' }, n.name), h('span', { class: 'mono small' }, [n.mac, ...(n.ips || [])].join('  '))]));
    }

    return poll(async () => {
      const r = await api('GET', `/api/devices/${enc(id)}/history?since=${since}`);
      if (r.points.length) {
        points = points.concat(r.points);
        if (points.length > 1800) points = points.slice(-1800);
        since = points[points.length - 1].t;
      }
      render(r.device);
      const last = points[points.length - 1];
      cpuC.draw([{ data: points.map((p) => [p.t, p.cpu]), color: '--accent' }], { max: 100 }, last ? fmtPct(last.cpu) : '—');
      memC.draw([{ data: points.map((p) => [p.t, p.mem]), color: '--accent-2' }], { max: 100 }, last ? fmtPct(last.mem) : '—');
      netC.draw([{ data: points.map((p) => [p.t, p.rx]), color: '--ok' }, { data: points.map((p) => [p.t, p.tx]), color: '--warn' }],
        { fmt: fmtRate }, last ? `↓ ${fmtRate(last.rx)}  ↑ ${fmtRate(last.tx)}` : '—');
    }, 2000);
  }

  // --- консоль ---
  function tabTerminal(body, dev) {
    if (!dev.online) { body.append(offlineNote(dev)); return null; }
    const shellSel = h('select', null, h('option', { value: 'powershell' }, 'Windows PowerShell'), h('option', { value: 'cmd' }, 'Командная строка (cmd)'), h('option', { value: 'pwsh' }, 'PowerShell 7 (pwsh)'));
    const status = h('span', { class: 'muted small' });
    const box = h('div', { class: 'term-box' });
    body.append(h('div', { class: 'toolbar' }, h('label', { class: 'inline' }, 'Оболочка', shellSel),
      h('button', { class: 'btn sm', onclick: () => connect() }, 'Новый сеанс'), status), box);
    let term = null, fit = null, ws = null, ro = null;
    const te = new TextEncoder();

    function disconnect() {
      if (ro) ro.disconnect();
      if (ws) { ws.onclose = null; ws.onmessage = null; try { ws.close(); } catch {} }
      if (term) term.dispose();
      box.replaceChildren();
      term = fit = ws = ro = null;
    }
    function connect() {
      disconnect();
      if (typeof Terminal === 'undefined') { box.append(h('div', { class: 'muted' }, 'Не удалось загрузить xterm.js')); return; }
      term = new Terminal({
        fontFamily: '"Cascadia Mono", Consolas, "Courier New", monospace', fontSize: 14, cursorBlink: true, scrollback: 5000,
        theme: { background: '#0b0e13', foreground: '#d6dde6', cursor: '#4f8cff', selectionBackground: '#264f78' },
      });
      fit = new FitAddon.FitAddon();
      term.loadAddon(fit);
      term.open(box);
      try { fit.fit(); } catch {}
      const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
      const sock = new WebSocket(`${scheme}://${location.host}/api/devices/${enc(dev.id)}/terminal?shell=${shellSel.value}&cols=${term.cols}&rows=${term.rows}`);
      sock.binaryType = 'arraybuffer';
      ws = sock;
      const t = term;
      status.textContent = 'Подключение…';
      sock.onopen = () => { status.textContent = 'Подключено'; t.focus(); };
      sock.onmessage = (e) => {
        if (typeof e.data === 'string') {
          try { const m = JSON.parse(e.data); if (m.type === 'error') t.write(`\r\n\x1b[31m${m.message}\x1b[0m\r\n`); } catch {}
        } else t.write(new Uint8Array(e.data));
      };
      sock.onclose = () => { status.textContent = 'Сеанс завершён'; t.write('\r\n\x1b[90m[сеанс завершён — нажмите «Новый сеанс»]\x1b[0m\r\n'); };
      t.onData((d) => { if (sock.readyState === 1) sock.send(te.encode(d)); });
      t.onResize(({ cols, rows }) => { if (sock.readyState === 1) sock.send(JSON.stringify({ type: 'resize', cols, rows })); });
      ro = new ResizeObserver(() => { try { fit.fit(); } catch {} });
      ro.observe(box);
    }
    connect();
    return disconnect;
  }

  // --- удалённый рабочий стол ---
  const KEY_COMBOS = {
    win: [['MetaLeft', 91]], alttab: [['AltLeft', 18], ['Tab', 9]], ctrlshiftesc: [['ControlLeft', 17], ['ShiftLeft', 16], ['Escape', 27]],
    altf4: [['AltLeft', 18], ['F4', 115]], winr: [['MetaLeft', 91], ['KeyR', 82]], wind: [['MetaLeft', 91], ['KeyD', 68]],
    winl: [['MetaLeft', 91], ['KeyL', 76]], prtsc: [['PrintScreen', 44]],
  };
  const pref = (k, d) => { try { return localStorage.getItem('rd.' + k) || d; } catch { return d; } };
  const setPref = (k, v) => { try { localStorage.setItem('rd.' + k, v); } catch {} };

  function tabDesktop(body, dev) {
    if (!dev.online) { body.append(offlineNote(dev)); return null; }
    const opt = (v, l) => h('option', { value: v }, l);
    const monSel = h('select', { title: 'Монитор' }, opt('-1', 'Основной монитор'));
    const qSel = h('select', { title: 'Качество изображения' }, opt('95', 'Качество: максимум'), opt('85', 'Качество: высокое'), opt('70', 'Качество: среднее'), opt('50', 'Качество: низкое'), opt('30', 'Качество: минимум'));
    const scaleSel = h('select', { title: 'Разрешение передачи' }, opt('100', 'Разрешение 100%'), opt('50', 'Разрешение 50%'));
    const fpsSel = h('select', { title: 'Частота кадров' }, opt('60', '60 к/с'), opt('30', '30 к/с'), opt('15', '15 к/с'));
    qSel.value = pref('q', '70'); scaleSel.value = pref('scale', '100'); fpsSel.value = pref('fps', '60');
    // профили под тип связи: по локальной сети — максимум качества и кадров, через интернет — экономно
    const PROFILES = { lan: { q: '85', fps: '60', scale: '100' }, net: { q: '50', fps: '30', scale: '100' } };
    const profSel = h('select', { title: 'Профиль передачи' }, opt('auto', 'Авто: по типу связи'), opt('lan', 'Локальная сеть — максимум'),
      opt('net', 'Интернет — экономно'), opt('custom', 'Свои настройки'));
    profSel.value = pref('profile', 'auto');
    const routeProfile = () => (dev.route === 'lan' ? 'lan' : 'net');
    const applyProfile = () => {
      const p = PROFILES[profSel.value === 'auto' ? routeProfile() : profSel.value];
      if (!p) return;
      qSel.value = p.q; fpsSel.value = p.fps; scaleSel.value = p.scale;
    };
    applyProfile();
    const viewOnly = h('input', { type: 'checkbox' });
    // способ сжатия: видео H.264 (сжимает видеокарта удалённого ПК, 60 к/с) или JPEG-плитки
    const codecSel = h('select', { title: 'Сжатие картинки. H.264 сжимает видеокарта удалённого ПК — плавно и до 60 к/с; JPEG — запасной вариант' },
      opt('auto', 'Видео H.264'), opt('jpeg', 'JPEG (совместимость)'));
    codecSel.value = pref('codec', 'auto');
    // игровой режим: мышь захватывается и передаётся относительными движениями (для шутеров и игр,
    // которые прячут курсор и читают «сырой» ввод)
    const gameBox = h('input', { type: 'checkbox', checked: pref('game', '0') === '1' });
    const sensSel = h('select', { title: 'Чувствительность мыши в игровом режиме' },
      ['0.5', '0.75', '1', '1.5', '2', '3'].map((v) => opt(v, 'мышь ×' + v)));
    sensSel.value = pref('sens', '1');
    const lockHint = h('div', { class: 'rd-lockhint', hidden: true });
    const fitBox = h('input', { type: 'checkbox', checked: pref('fit', '1') === '1' });
    const keysSel = h('select', { title: 'Отправить сочетание клавиш' }, opt('', 'Сочетания клавиш…'), opt('win', 'Win'), opt('alttab', 'Alt+Tab'),
      opt('ctrlshiftesc', 'Ctrl+Shift+Esc'), opt('altf4', 'Alt+F4'), opt('winr', 'Win+R'), opt('wind', 'Win+D'), opt('prtsc', 'PrintScreen'));
    const stats = h('span', { class: 'muted small mono rd-stats' }, '');
    const canvas = h('canvas', { class: 'rd-canvas', tabindex: '0', width: '1280', height: '720' });
    const overlay = h('div', { class: 'rd-overlay' }, 'Подключение…');
    const stage = h('div', { class: 'rd-stage' + (fitBox.checked ? ' fit' : '') }, canvas);
    // подсказки лежат вне прокручиваемой области, поэтому всегда по центру видимой картинки
    const view = h('div', { class: 'rd-view' }, stage, overlay, lockHint);
    // сочетание клавиш для включения/выключения игрового режима (настраивается)
    const keyName = (k) => !k || !k.code ? 'нет' : [k.ctrl && 'Ctrl', k.alt && 'Alt', k.shift && 'Shift', k.meta && 'Win',
      k.code.replace(/^Key/, '').replace(/^Digit/, '').replace(/^Numpad/, 'Num ')].filter(Boolean).join('+');
    let gameKey;
    try { gameKey = JSON.parse(pref('gamekey', '') || 'null'); } catch { gameKey = null; }
    if (gameKey === null) gameKey = { ctrl: true, alt: true, shift: false, meta: false, code: 'KeyG' };
    const gkBtn = h('button', { class: 'btn sm ghost', title: 'Сочетание клавиш для включения и выключения игрового режима. Нажмите, затем введите новое сочетание (Esc — отмена, Backspace — без сочетания)' }, '⌨ ' + keyName(gameKey));
    let recording = false;
    const fsBtn = h('button', { class: 'btn sm', title: 'Во весь экран (Win и Alt+Tab тоже уходят на удалённый ПК)' }, 'Во весь экран');
    const pasteBtn = h('button', { class: 'btn sm', title: 'Набрать текст на удалённом ПК' }, 'Вставить текст');
    const reBtn = h('button', { class: 'btn sm ghost' }, 'Переподключить');
    body.append(
      h('div', { class: 'toolbar' }, profSel, monSel, qSel, scaleSel, fpsSel, codecSel, keysSel, pasteBtn, fsBtn, reBtn),
      h('div', { class: 'toolbar' }, h('label', { class: 'inline' }, fitBox, 'вписать в окно'), h('label', { class: 'inline' }, viewOnly, 'только просмотр'),
        h('label', { class: 'inline game-toggle', title: 'Для игр: мышь захватывается, движения передаются как от настоящей мыши' }, gameBox, '🎮 игровой режим'), gkBtn, sensSel, stats),
      view);

    const ctx = canvas.getContext('2d', { alpha: false, desynchronized: true });
    let ws = null, chain = Promise.resolve(), frames = 0, bytes = 0, rtt = null, capKind = '', desk = '';
    const pressed = new Map();
    const send = (o) => { if (ws && ws.readyState === 1) ws.send(JSON.stringify(o)); };
    // ---- видео H.264 через WebCodecs (нужен защищённый адрес: https или localhost) ----
    let canH264 = false, codecInfo = '', vdec = null, vcodec = '', vsize = '', vids = [], vNeedKey = true, lastKeyReq = 0;
    const h264Ready = (async () => {
      try {
        if (!window.VideoDecoder) return false;
        const r = await VideoDecoder.isConfigSupported({ codec: 'avc1.640033', optimizeForLatency: true });
        canH264 = !!r.supported;
      } catch { canH264 = false; }
      return canH264;
    })();
    const wantCodec = () => (codecSel.value === 'jpeg' || !canH264 ? 'jpeg' : 'h264');
    const sendCfg = (extra) => send({ t: 'cfg', q: +qSel.value, fps: +fpsSel.value, scale: +scaleSel.value, codec: wantCodec(), ...extra });
    const ackId = (sock, id) => { if (sock.readyState === 1) sock.send(JSON.stringify({ t: 'ack', id })); };
    const askKey = () => { if (performance.now() - lastKeyReq > 500) { lastKeyReq = performance.now(); send({ t: 'key' }); } };
    // профиль/уровень из SPS → строка кодека для декодера (avc1.PPCCLL)
    const avcCodec = (d) => {
      for (let i = 0; i + 6 < d.length; i++) {
        if (d[i] === 0 && d[i + 1] === 0 && d[i + 2] === 1 && (d[i + 3] & 0x1f) === 7) {
          return 'avc1.' + [d[i + 4], d[i + 5], d[i + 6]].map((b) => b.toString(16).padStart(2, '0')).join('');
        }
      }
      return '';
    };
    const closeDecoder = () => {
      if (vdec && vdec.state !== 'closed') { try { vdec.close(); } catch {} }
      vdec = null; vids = []; vNeedKey = true;
    };
    function makeDecoder(sock, cs, W, H) {
      closeDecoder();
      const dec = new VideoDecoder({
        output: (frame) => {
          if (sock === ws) {
            if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) { canvas.width = frame.displayWidth; canvas.height = frame.displayHeight; }
            ctx.drawImage(frame, 0, 0);
            frames++;
            overlay.hidden = true;
          }
          frame.close();
          const id = vids.shift();
          if (id !== undefined) ackId(sock, id);
        },
        error: (e) => {
          console.warn('H.264:', e);
          if (vdec !== dec) return;
          vids.forEach((id) => ackId(sock, id));
          vdec = null; vids = []; vNeedKey = true;
          askKey();
        },
      });
      dec.configure({ codec: cs, codedWidth: W, codedHeight: H, optimizeForLatency: true });
      vdec = dec; vcodec = cs; vsize = W + 'x' + H;
    }
    function onVideo(sock, buf, dv) {
      const id = dv.getUint32(1, true), W = dv.getUint16(5, true), H = dv.getUint16(7, true), key = (dv.getUint8(9) & 1) === 1;
      const data = new Uint8Array(buf, 10);
      if (key) {
        const cs = avcCodec(data) || 'avc1.640033';
        if (!vdec || vdec.state !== 'configured' || cs !== vcodec || vsize !== W + 'x' + H) {
          try { makeDecoder(sock, cs, W, H); } catch (e) { console.warn(e); closeDecoder(); }
        }
      }
      if (!vdec || vdec.state !== 'configured' || (vNeedKey && !key)) { ackId(sock, id); askKey(); return; }
      vNeedKey = false;
      vids.push(id);
      try {
        vdec.decode(new EncodedVideoChunk({ type: key ? 'key' : 'delta', timestamp: id * 16667, data }));
      } catch (e) {
        console.warn(e); vids.pop(); ackId(sock, id); closeDecoder(); askKey();
      }
    }

    function onText(sock, m) {
      if (m.type === 'pong') rtt = performance.now() - m.ts;
      else if (m.type === 'info') {
        capKind = m.capture; desk = m.desktop || '';
        const cur = String(m.monitor);
        monSel.replaceChildren(...(m.monitors || []).map((x) => opt(String(x.index), x.name + (x.primary ? ' · основной' : ''))));
        monSel.value = cur;
      } else if (m.type === 'codec') {
        codecInfo = m.codec === 'h264' ? 'H.264' + (m.encoder ? ' (' + m.encoder.replace(/ ?H\.?264 ?|Encoder|MFT/gi, ' ').replace(/\s+/g, ' ').trim() + ')' : '') : 'JPEG';
        if (m.codec !== 'h264') closeDecoder();
        if (m.reason && codecSel.value !== 'jpeg') toast('Видео H.264 недоступно на удалённом ПК, используется JPEG: ' + m.reason, 'bad');
      } else if (m.type === 'error') { overlay.textContent = m.message; overlay.hidden = false; }
    }

    function connect() {
      disconnect();
      overlay.textContent = 'Подключение…'; overlay.hidden = false;
      const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
      const sock = new WebSocket(`${scheme}://${location.host}/api/devices/${enc(dev.id)}/desktop`);
      sock.binaryType = 'arraybuffer';
      ws = sock;
      chain = Promise.resolve();
      sock.onopen = () => { h264Ready.then(() => { if (sock === ws) sendCfg(); }); };
      sock.onmessage = (e) => {
        if (typeof e.data === 'string') { try { onText(sock, JSON.parse(e.data)); } catch {} return; }
        const buf = e.data, dv = new DataView(buf);
        if (dv.getUint8(0) === 2) { bytes += buf.byteLength; onVideo(sock, buf, dv); return; }
        if (dv.getUint8(0) !== 1) return;
        bytes += buf.byteLength;
        const id = dv.getUint32(1, true), W = dv.getUint16(5, true), H = dv.getUint16(7, true), n = dv.getUint16(9, true);
        const tiles = [];
        let o = 11;
        for (let i = 0; i < n; i++) {
          const x = dv.getUint16(o, true), y = dv.getUint16(o + 2, true), len = dv.getUint32(o + 8, true);
          o += 12;
          tiles.push(createImageBitmap(new Blob([new Uint8Array(buf, o, len)], { type: 'image/jpeg' })).then((bm) => ({ bm, x, y })));
          o += len;
        }
        const decoded = Promise.all(tiles); // декодирование параллельно, отрисовка строго по порядку
        chain = chain.then(() => decoded).then((list) => {
          if (sock !== ws) { list.forEach((t) => t.bm.close()); return; }
          if (canvas.width !== W || canvas.height !== H) { canvas.width = W; canvas.height = H; }
          for (const t of list) { ctx.drawImage(t.bm, t.x, t.y); t.bm.close(); }
          frames++;
          overlay.hidden = true;
          if (sock.readyState === 1) sock.send(JSON.stringify({ t: 'ack', id }));
        }).catch((err) => { console.warn(err); if (sock.readyState === 1) sock.send(JSON.stringify({ t: 'ack', id })); });
      };
      sock.onclose = () => {
        if (sock !== ws) return;
        overlay.textContent = 'Соединение закрыто — нажмите «Переподключить»';
        overlay.hidden = false;
      };
    }
    function disconnect() {
      if (ws) { const s = ws; ws = null; s.onclose = null; try { s.close(); } catch {} }
      pressed.clear();
      closeDecoder(); codecInfo = '';
    }

    // статистика и замер задержки
    const timer = setInterval(() => {
      send({ t: 'ping', ts: performance.now() });
      if (ws && ws.readyState === 1) {
        stats.textContent = [ROUTES[dev.route] ? ROUTES[dev.route][0] : '', rtt == null ? 'RTT —' : `RTT ${rtt.toFixed(rtt < 10 ? 1 : 0)} мс`, `${frames} к/с`, fmtRate(bytes),
          canvas.width + '×' + canvas.height, capKind, codecInfo, desk && desk.toLowerCase() !== 'default' ? 'рабочий стол: ' + desk : ''].filter(Boolean).join(' · ');
      }
      frames = 0; bytes = 0;
    }, 1000);

    // ---- ввод ----
    // координаты на удалённом экране; в полноэкранном режиме картинка вписана с полями (object-fit: contain)
    const pos = (e) => {
      const r = canvas.getBoundingClientRect();
      const k = Math.min(r.width / canvas.width, r.height / canvas.height) || 1;
      const ox = (r.width - canvas.width * k) / 2, oy = (r.height - canvas.height * k) / 2;
      const x = Math.round((e.clientX - r.left - ox) / k), y = Math.round((e.clientY - r.top - oy) / k);
      return { x: Math.max(0, Math.min(canvas.width - 1, x)), y: Math.max(0, Math.min(canvas.height - 1, y)) };
    };
    const active = () => !viewOnly.checked && ws && ws.readyState === 1;
    const locked = () => document.pointerLockElement === canvas;
    const lockMouse = () => {
      if (!gameBox.checked || locked()) return;
      try {
        const r = canvas.requestPointerLock({ unadjustedMovement: true }); // «сырые» движения без ускорения ОС
        if (r && r.catch) r.catch(() => { try { const r2 = canvas.requestPointerLock(); if (r2 && r2.catch) r2.catch(() => {}); } catch {} });
      } catch { try { canvas.requestPointerLock(); } catch {} }
    };
    // относительные движения копятся с дробной частью, чтобы чувствительность ×0.5 не теряла шаги
    let accX = 0, accY = 0;
    const moveRel = (e) => {
      if (!locked() || !active()) return;
      const k = +sensSel.value || 1;
      accX += e.movementX * k; accY += e.movementY * k;
      const dx = Math.trunc(accX), dy = Math.trunc(accY);
      if (!dx && !dy) return;
      accX -= dx; accY -= dy;
      send({ t: 'mr', dx, dy });
    };
    const rawMoves = 'onpointerrawupdate' in canvas; // самый частый поток движений (Chrome/Edge)
    canvas.addEventListener(rawMoves ? 'pointerrawupdate' : 'pointermove', moveRel);
    canvas.addEventListener('pointermove', (e) => { if (!locked() && active()) send({ t: 'mm', ...pos(e) }); });
    canvas.addEventListener('pointerdown', (e) => {
      canvas.focus();
      // в игровом режиме сначала захват мыши; привязку указателя (pointer capture) не включаем —
      // с ней браузер может отказать в захвате
      if (gameBox.checked && !locked()) { e.preventDefault(); lockMouse(); return; }
      if (!locked() && !gameBox.checked) { try { canvas.setPointerCapture(e.pointerId); } catch {} }
    });
    canvas.addEventListener('mousedown', (e) => {
      e.preventDefault(); canvas.focus();
      if (gameBox.checked && !locked()) return; // этот щелчок только захватывает мышь
      if (active()) send(locked() ? { t: 'md', b: e.button, r: true } : { t: 'md', b: e.button, ...pos(e) });
    });
    canvas.addEventListener('mouseup', (e) => {
      e.preventDefault();
      if (active()) send(locked() ? { t: 'mu', b: e.button, r: true } : { t: 'mu', b: e.button, ...pos(e) });
    });
    const onLock = () => {
      const on = locked();
      stage.classList.toggle('game-locked', on);
      if (!on) { accX = accY = 0; send({ t: 'rel' }); pressed.clear(); }
      lockHint.hidden = !(gameBox.checked);
      lockHint.textContent = on
        ? (document.fullscreenElement ? '🎮 Мышь захвачена · удерживайте Esc, чтобы выйти' : '🎮 Мышь захвачена · Esc — освободить')
        : '🎮 Игровой режим · щёлкните по картинке, чтобы захватить мышь';
      lockHint.classList.toggle('on', on);
    };
    document.addEventListener('pointerlockchange', onLock);
    const onLockErr = () => toast('Браузер не дал захватить мышь. Щёлкните по картинке ещё раз (в полноэкранном режиме — после входа в него).', 'bad');
    document.addEventListener('pointerlockerror', onLockErr);
    gameBox.addEventListener('change', () => {
      setPref('game', gameBox.checked ? '1' : '0');
      sensSel.hidden = !gameBox.checked;
      if (!gameBox.checked && locked()) document.exitPointerLock();
      onLock();
      canvas.focus();
    });
    sensSel.addEventListener('change', () => { setPref('sens', sensSel.value); canvas.focus(); });
    sensSel.hidden = !gameBox.checked;
    onLock();
    canvas.addEventListener('contextmenu', (e) => e.preventDefault());
    canvas.addEventListener('wheel', (e) => {
      e.preventDefault();
      if (!active()) return;
      const k = e.deltaMode === 1 ? 40 : e.deltaMode === 2 ? 800 : 1;
      send({ t: 'wh', dy: Math.round(-e.deltaY * k * 1.2), dx: Math.round(e.deltaX * k * 1.2) });
    }, { passive: false });
    // запись нового сочетания и переключение игрового режима — раньше, чем клавиши уйдут на удалённый ПК
    const isMod = (c) => /^(Control|Alt|Shift|Meta|OS)(Left|Right)?$/.test(c);
    const onKey = (e) => {
      if (recording) {
        e.preventDefault(); e.stopPropagation();
        if (isMod(e.code)) return;
        recording = false; gkBtn.classList.remove('active');
        if (e.code === 'Escape') { gkBtn.textContent = '⌨ ' + keyName(gameKey); return; }
        gameKey = e.code === 'Backspace' ? { code: '' } : { ctrl: e.ctrlKey, alt: e.altKey, shift: e.shiftKey, meta: e.metaKey, code: e.code };
        if (gameKey.code && !gameKey.ctrl && !gameKey.alt && !gameKey.meta && !/^F\d+$/.test(gameKey.code)) {
          toast('Добавьте Ctrl, Alt или Win — иначе клавиша не дойдёт до игры', 'bad');
          gameKey = JSON.parse(pref('gamekey', '') || 'null') || { ctrl: true, alt: true, code: 'KeyG' };
        }
        setPref('gamekey', JSON.stringify(gameKey));
        gkBtn.textContent = '⌨ ' + keyName(gameKey);
        return;
      }
      const k = gameKey;
      if (!k || !k.code || e.code !== k.code || e.repeat) return;
      if (!!k.ctrl !== e.ctrlKey || !!k.alt !== e.altKey || !!k.shift !== e.shiftKey || !!k.meta !== e.metaKey) return;
      // переключаем, только когда работа идёт с удалённым экраном
      if (document.activeElement !== canvas && !locked() && document.fullscreenElement !== view) return;
      e.preventDefault(); e.stopPropagation();
      // уже отправленные модификаторы отпускаем, чтобы они не «залипли» на удалённом ПК
      for (const [c, kc] of pressed) send({ t: 'ku', c, k: kc });
      pressed.clear();
      gameBox.checked = !gameBox.checked;
      gameBox.dispatchEvent(new Event('change'));
      if (gameBox.checked) lockMouse(); // нажатие клавиши даёт право захватить мышь
      toast(gameBox.checked ? '🎮 Игровой режим включён' : 'Игровой режим выключен');
    };
    document.addEventListener('keydown', onKey, true);
    gkBtn.addEventListener('click', () => {
      recording = !recording;
      gkBtn.classList.toggle('active', recording);
      gkBtn.textContent = recording ? '⌨ нажмите сочетание…' : '⌨ ' + keyName(gameKey);
    });
    canvas.addEventListener('keydown', (e) => {
      if (!active()) return;
      e.preventDefault();
      pressed.set(e.code, e.keyCode);
      send({ t: 'kd', c: e.code, k: e.keyCode });
    });
    canvas.addEventListener('keyup', (e) => {
      if (!active()) return;
      e.preventDefault();
      pressed.delete(e.code);
      send({ t: 'ku', c: e.code, k: e.keyCode });
    });
    canvas.addEventListener('blur', () => { if (pressed.size) { send({ t: 'rel' }); pressed.clear(); } });

    // ---- панель ----
    const custom = () => { profSel.value = 'custom'; setPref('profile', 'custom'); };
    qSel.addEventListener('change', () => { setPref('q', qSel.value); custom(); sendCfg(); });
    fpsSel.addEventListener('change', () => { setPref('fps', fpsSel.value); custom(); sendCfg(); });
    scaleSel.addEventListener('change', () => { setPref('scale', scaleSel.value); custom(); sendCfg(); });
    profSel.addEventListener('change', () => {
      setPref('profile', profSel.value);
      if (profSel.value === 'custom') { qSel.value = pref('q', '70'); fpsSel.value = pref('fps', '60'); scaleSel.value = pref('scale', '100'); }
      else applyProfile();
      sendCfg();
      canvas.focus();
    });
    monSel.addEventListener('change', () => sendCfg({ mon: +monSel.value }));
    codecSel.addEventListener('change', () => {
      setPref('codec', codecSel.value);
      if (codecSel.value !== 'jpeg' && !canH264) toast(window.isSecureContext ? 'Этот браузер не умеет декодировать H.264 — остаётся JPEG' : 'Для H.264 откройте панель по https:// или через localhost — сейчас остаётся JPEG', 'bad');
      sendCfg();
    });
    fitBox.addEventListener('change', () => { setPref('fit', fitBox.checked ? '1' : '0'); stage.classList.toggle('fit', fitBox.checked); });
    keysSel.addEventListener('change', () => {
      const c = KEY_COMBOS[keysSel.value];
      keysSel.value = '';
      if (!c || !active()) return;
      c.forEach(([code, k]) => send({ t: 'kd', c: code, k }));
      [...c].reverse().forEach(([code, k]) => send({ t: 'ku', c: code, k }));
      canvas.focus();
    });
    pasteBtn.addEventListener('click', async () => {
      const ta = h('textarea', { rows: 6, placeholder: 'Текст будет набран на удалённом ПК в активном окне' });
      const text = await modal('Вставить текст', ta, [{ label: 'Отмена', value: null }, { label: 'Набрать', kind: 'primary', submit: true, getValue: () => ta.value }]);
      if (text) { send({ t: 'text', s: text }); canvas.focus(); }
    });
    fsBtn.addEventListener('click', async () => {
      try {
        await view.requestFullscreen();
        if (navigator.keyboard && navigator.keyboard.lock) await navigator.keyboard.lock().catch(() => {});
        canvas.focus();
        lockMouse(); // в игровом режиме мышь захватывается сразу
      } catch (e) { toast('Полноэкранный режим недоступен: ' + e.message, 'bad'); }
    });
    const onFs = () => { if (!document.fullscreenElement && navigator.keyboard && navigator.keyboard.unlock) navigator.keyboard.unlock(); onLock(); };
    document.addEventListener('fullscreenchange', onFs);
    reBtn.addEventListener('click', connect);

    connect();
    return () => {
      clearInterval(timer);
      document.removeEventListener('fullscreenchange', onFs);
      document.removeEventListener('keydown', onKey, true);
      document.removeEventListener('pointerlockchange', onLock);
      document.removeEventListener('pointerlockerror', onLockErr);
      if (locked()) document.exitPointerLock();
      if (document.fullscreenElement === view) document.exitFullscreen().catch(() => {});
      disconnect();
    };
  }

  // --- таблица с сортировкой (процессы, службы) ---
  function sortableTable(cols, defSort) {
    let sort = defSort;
    const ths = cols.map((c) => h('th', {
      class: 'sortable' + (c.num ? ' num' : ''),
      onclick: () => { sort = { key: c.key, dir: sort.key === c.key ? -sort.dir : (c.num ? -1 : 1) }; onSort(); },
    }, c.label));
    const tbody = h('tbody');
    const table = h('table', { class: 'tbl' }, h('thead', null, h('tr', null, ths, h('th', null, ''))), tbody);
    let onSort = () => {};
    return {
      table, tbody,
      set onSort(fn) { onSort = fn; },
      sortRows(rows) {
        ths.forEach((th, i) => { const on = cols[i].key === sort.key; th.classList.toggle('sorted', on); th.setAttribute('data-dir', on ? (sort.dir > 0 ? '▲' : '▼') : ''); });
        return rows.sort((a, b) => {
          const x = a[sort.key], y = b[sort.key];
          return (typeof x === 'string' ? x.localeCompare(y || '', 'ru', { sensitivity: 'base' }) : (x || 0) - (y || 0)) * sort.dir;
        });
      },
    };
  }

  // --- процессы ---
  function tabProcesses(body, dev) {
    if (!dev.online) { body.append(offlineNote(dev)); return null; }
    const q = h('input', { type: 'search', placeholder: 'Поиск: имя, PID, пользователь' });
    const auto = h('input', { type: 'checkbox', checked: true });
    const info = h('span', { class: 'muted small' });
    const t = sortableTable([
      { key: 'name', label: 'Имя' }, { key: 'pid', label: 'PID', num: true }, { key: 'user', label: 'Пользователь' },
      { key: 'cpu', label: 'ЦП', num: true }, { key: 'mem', label: 'Память', num: true }, { key: 'threads', label: 'Потоки', num: true },
    ], { key: 'cpu', dir: -1 });
    body.append(h('div', { class: 'toolbar' }, q, h('label', { class: 'inline' }, auto, 'автообновление'),
      h('button', { class: 'btn sm', onclick: () => load() }, 'Обновить'), info), h('div', { class: 'table-wrap' }, t.table));
    let data = [], hover = false;
    t.tbody.addEventListener('mouseenter', () => { hover = true; });
    t.tbody.addEventListener('mouseleave', () => { hover = false; });

    function render() {
      const f = q.value.trim().toLowerCase();
      const rows = t.sortRows(data.filter((p) => !f || p.name.toLowerCase().includes(f) || String(p.pid) === f || (p.user || '').toLowerCase().includes(f)));
      info.textContent = `Процессов: ${data.length}` + (f ? ` · найдено: ${rows.length}` : '') + (rows.length > 400 ? ' · показаны первые 400' : '');
      t.tbody.replaceChildren(...rows.slice(0, 400).map((p) => h('tr', null,
        h('td', { class: 'ell', title: p.name }, p.name), h('td', { class: 'num' }, p.pid), h('td', { class: 'ell' }, p.user || '—'),
        h('td', { class: 'num' }, p.cpu >= 0.05 ? p.cpu.toFixed(1) + '%' : '0'), h('td', { class: 'num' }, p.mem ? fmtBytes(p.mem) : '—'),
        h('td', { class: 'num' }, p.threads),
        h('td', { class: 'row-act' },
          h('button', { class: 'btn xs danger-ghost', onclick: () => kill(p, false) }, 'Завершить'),
          h('button', { class: 'btn xs ghost', title: 'Завершить вместе с дочерними процессами', onclick: () => kill(p, true) }, 'Дерево')))));
    }
    async function kill(p, tree) {
      if (!await confirmDlg('Завершить процесс?', `${p.name} (PID ${p.pid})${tree ? ' и все его дочерние процессы будут завершены' : ' будет завершён'} без сохранения данных.`, 'Завершить', 'danger')) return;
      try { await api('POST', rpc(dev.id, 'kill'), { pid: p.pid, tree }); toast(`${p.name} завершён`, 'ok'); load(true); } catch (e) { toast(e.message, 'bad'); }
    }
    async function load(force) {
      data = await api('POST', rpc(dev.id, 'procs'));
      if (force || !hover) render();
    }
    q.addEventListener('input', render);
    t.onSort = render;
    return poll(async () => { if (auto.checked || !data.length) await load(); }, 3000);
  }

  // --- службы ---
  const SVC_STATE = { running: 'работает', stopped: 'остановлена', starting: 'запускается', stopping: 'останавливается', paused: 'приостановлена', pausing: 'приостанавливается', continuing: 'возобновляется' };
  function tabServices(body, dev) {
    if (!dev.online) { body.append(offlineNote(dev)); return null; }
    const q = h('input', { type: 'search', placeholder: 'Поиск по имени или описанию' });
    const filter = h('select', null, h('option', { value: '' }, 'Все'), h('option', { value: 'running' }, 'Работают'), h('option', { value: 'stopped' }, 'Остановлены'));
    const info = h('span', { class: 'muted small' });
    const t = sortableTable([
      { key: 'name', label: 'Имя' }, { key: 'display', label: 'Описание' }, { key: 'state', label: 'Состояние' }, { key: 'pid', label: 'PID', num: true },
    ], { key: 'display', dir: 1 });
    body.append(h('div', { class: 'toolbar' }, q, filter, h('button', { class: 'btn sm', onclick: () => load() }, 'Обновить'), info),
      h('div', { class: 'table-wrap' }, t.table));
    let data = [];
    const busy = new Set();

    function render() {
      const f = q.value.trim().toLowerCase(), fs = filter.value;
      const rows = t.sortRows(data.filter((s) => (!fs || s.state === fs) && (!f || s.name.toLowerCase().includes(f) || s.display.toLowerCase().includes(f))));
      info.textContent = `Служб: ${data.length} · работают: ${data.filter((s) => s.state === 'running').length}` + (f || fs ? ` · показано: ${rows.length}` : '');
      t.tbody.replaceChildren(...rows.map((s) => {
        const b = busy.has(s.name);
        const act = (a, label, cls) => h('button', { class: 'btn xs ' + cls, disabled: b, onclick: () => control(s, a, label) }, label);
        return h('tr', null,
          h('td', { class: 'ell mono small', title: s.name }, s.name), h('td', { class: 'ell', title: s.display }, s.display),
          h('td', null, h('span', { class: 'state ' + s.state }, b ? 'выполняется…' : SVC_STATE[s.state] || s.state)),
          h('td', { class: 'num' }, s.pid || ''),
          h('td', { class: 'row-act' }, s.state === 'stopped' ? act('start', 'Запустить', 'ghost')
            : s.state === 'running' ? [act('restart', 'Перезапустить', 'ghost'), act('stop', 'Остановить', 'danger-ghost')] : null));
      }));
    }
    async function control(s, action, label) {
      if (action !== 'start' && !await confirmDlg(label + ' службу?', `${s.display} (${s.name})`, label, 'danger')) return;
      busy.add(s.name); render();
      try { await api('POST', rpc(dev.id, 'service'), { name: s.name, action }); toast(`${s.display}: готово`, 'ok'); } catch (e) { toast(e.message, 'bad'); }
      busy.delete(s.name);
      load();
    }
    async function load() { data = await api('POST', rpc(dev.id, 'services')); render(); }
    q.addEventListener('input', render);
    filter.addEventListener('change', render);
    t.onSort = render;
    load().catch((e) => toast(e.message, 'bad'));
    return null;
  }

  // --- выполнение команд ---
  function resultCard(title, command) {
    const meta = h('span', { class: 'muted small right' }, 'выполняется…');
    const out = h('pre', { class: 'out muted' }, '…');
    const el = h('div', { class: 'card result' }, h('div', { class: 'result-head' }, h('b', null, title), h('code', { class: 'ell' }, command), meta), out);
    return {
      el,
      set(r) {
        out.classList.remove('muted');
        out.textContent = r.output && r.output.trim() ? r.output.replace(/\s+$/, '') : '(нет вывода)';
        meta.textContent = `код ${r.exit_code} · ${(r.duration_ms / 1000).toFixed(1)} с` + (r.timed_out ? ' · превышено время' : '') + (r.truncated ? ' · вывод обрезан' : '');
        el.classList.add(r.exit_code === 0 && !r.timed_out ? 'ok' : 'bad');
      },
      fail(m) { out.textContent = m; meta.textContent = 'ошибка'; el.classList.add('bad'); },
    };
  }

  function runForm(onRun, extra) {
    const shell = h('select', null, h('option', { value: 'powershell' }, 'PowerShell'), h('option', { value: 'cmd' }, 'cmd'));
    const timeout = h('input', { type: 'number', min: 1, max: 600, value: 60, class: 'num-in', title: 'Тайм-аут, с' });
    const cmd = h('textarea', { rows: 4, spellcheck: 'false', placeholder: 'Например: Get-Process | Sort-Object CPU -Descending | Select-Object -First 10\nCtrl+Enter — выполнить' });
    const btn = h('button', { class: 'btn primary', type: 'button' }, 'Выполнить');
    const go = async () => {
      const c = cmd.value.trim();
      if (!c || btn.disabled) return;
      btn.disabled = true;
      try { await onRun({ shell: shell.value, command: c, timeout: Math.min(600, Math.max(1, +timeout.value || 60)) }); } finally { btn.disabled = false; }
    };
    btn.addEventListener('click', go);
    cmd.addEventListener('keydown', (e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); go(); } });
    return h('div', { class: 'run-form' }, extra || null, cmd,
      h('div', { class: 'row' }, h('label', { class: 'inline' }, 'Оболочка', shell), h('label', { class: 'inline' }, 'Тайм-аут, с', timeout), btn));
  }

  function tabRun(body, dev) {
    if (!dev.online) { body.append(offlineNote(dev)); return null; }
    const results = h('div', { class: 'results' });
    body.append(runForm(async (req) => {
      const card = resultCard(dev.name, req.command);
      results.prepend(card.el);
      try { card.set(await api('POST', rpc(dev.id, 'exec'), req)); } catch (e) { card.fail(e.message); }
    }), results);
    return null;
  }

  // ---------- страница: массовое выполнение ----------
  function pageExec(view) {
    const picks = h('div', { class: 'pick-list' });
    const results = h('div', { class: 'results' });
    const selected = new Set();
    let devices = [];
    const allBox = h('input', { type: 'checkbox' });
    allBox.addEventListener('change', () => {
      devices.filter((d) => d.online).forEach((d) => (allBox.checked ? selected.add(d.id) : selected.delete(d.id)));
      renderPicks();
    });
    function renderPicks() {
      if (!devices.length) { picks.replaceChildren(h('span', { class: 'muted' }, 'Нет привязанных ПК')); return; }
      picks.replaceChildren(...devices.map((d) => {
        const cb = h('input', { type: 'checkbox', checked: selected.has(d.id), disabled: !d.online });
        cb.addEventListener('change', () => { cb.checked ? selected.add(d.id) : selected.delete(d.id); });
        return h('label', { class: 'pick' + (d.online ? '' : ' disabled') }, cb, h('span', { class: 'dot ' + (d.online ? 'on' : 'off') }), d.name);
      }));
    }
    view.append(h('div', { class: 'page-head' }, h('h1', null, 'Массовое выполнение')),
      h('p', { class: 'muted', style: { marginTop: '-8px' } }, 'Одна команда сразу на нескольких ПК. Результаты приходят по мере выполнения.'),
      h('div', { class: 'toolbar' }, h('label', { class: 'inline' }, allBox, 'выбрать все в сети')),
      picks,
      runForm(async (req) => {
        const targets = devices.filter((d) => selected.has(d.id) && d.online);
        if (!targets.length) { toast('Выберите хотя бы один ПК в сети', 'bad'); return; }
        results.replaceChildren();
        await Promise.all(targets.map(async (d) => {
          const card = resultCard(d.name, req.command);
          results.append(card.el);
          try { card.set(await api('POST', rpc(d.id, 'exec'), req)); } catch (e) { card.fail(e.message); }
        }));
      }),
      results);
    return poll(async () => {
      devices = await api('GET', '/api/devices');
      for (const id of [...selected]) if (!devices.some((d) => d.id === id && d.online)) selected.delete(id);
      renderPicks();
    }, 5000);
  }

  // ---------- страница: журнал ----------
  const ACTIONS = {
    login: 'Вход', login_failed: 'Неудачный вход', setup: 'Первичная настройка', password_changed: 'Смена пароля',
    enroll_key_rotated: 'Смена ключа привязки', device_enrolled: 'Привязан ПК', enroll_failed: 'Ошибка привязки ПК',
    device_deleted: 'Удалён ПК', rename: 'Переименование', kill: 'Завершение процесса', service: 'Управление службой',
    exec: 'Выполнение команды', power: 'Питание', wake: 'Wake-on-LAN', terminal_open: 'Открыта консоль', terminal_close: 'Закрыта консоль',
    desktop_open: 'Подключение к рабочему столу', desktop_close: 'Отключение от рабочего стола',
  };
  function pageAudit(view) {
    const tbody = h('tbody');
    view.append(h('div', { class: 'page-head' }, h('h1', null, 'Журнал действий'),
      h('div', { class: 'actions' }, h('button', { class: 'btn sm', onclick: () => load() }, 'Обновить'))),
      h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
        h('thead', null, h('tr', null, ['Время', 'Пользователь', 'IP', 'Устройство', 'Действие', 'Подробности'].map((x) => h('th', null, x)))), tbody)));
    async function load() {
      const rows = await api('GET', '/api/audit');
      tbody.replaceChildren(...(rows.length ? rows.map((e) => h('tr', null,
        h('td', { style: { whiteSpace: 'nowrap' } }, fmtTime(e.time)), h('td', null, e.user || '—'), h('td', { class: 'mono small' }, e.ip),
        h('td', null, e.device || '—'), h('td', null, ACTIONS[e.action] || e.action),
        h('td', { class: 'ell mono small', title: e.detail }, e.detail || ''))) : [h('tr', null, h('td', { colspan: 6, class: 'muted' }, 'Записей пока нет'))]));
    }
    load().catch((e) => toast(e.message, 'bad'));
    return null;
  }

  // ---------- страница: настройки ----------
  // ---------- страница: доступ (кто кем управляет, общий буфер обмена) ----------
  function pageAccess(view) {
    const box = h('div');
    view.append(h('div', { class: 'page-head' }, h('h1', null, 'Доступ')),
      h('p', { class: 'muted', style: { marginTop: '-8px' } }, 'Отметьте, каким ПК можно управлять другими. Без вашего разрешения обычные ПК друг друга не видят.'),
      box);
    const load = async () => {
      const [devs, acc] = await Promise.all([api('GET', '/api/devices'), api('GET', '/api/access')]);
      const grants = acc.grants || {};
      const save = async (body) => { await api('POST', '/api/access', body); };
      const others = devs.filter((d) => !d.self);
      // управлять можно и главным ПК — он идёт первым столбцом
      const targets = [...devs.filter((d) => d.self), ...others];
      const clipCard = h('div', { class: 'card' }, h('h3', null, 'Единый буфер обмена'),
        toggle('Общий буфер обмена для всех ПК', 'Текст, скопированный на одном ПК, можно вставить на любом другом. Отдельный ПК можно исключить на его странице.',
          acc.clip_shared, async (on) => { await save({ clip_shared: on }); toast(on ? 'Общий буфер обмена включён' : 'Общий буфер обмена выключен', 'ok'); }));
      if (!others.length) {
        box.replaceChildren(clipCard, h('div', { class: 'card', style: { marginTop: '14px' } }, h('h3', null, 'Кто кем управляет'),
          h('p', { class: 'muted' }, 'Пока к главному ПК не подключён ни один обычный ПК.')));
        return;
      }
      const table = h('table', { class: 'tbl access' },
        h('thead', null, h('tr', null, h('th', null, 'Кто управляет ↓ / кем →'),
          targets.map((t) => h('th', { class: 'center' }, t.name, t.self ? h('div', { class: 'muted small' }, 'главный ПК') : null)))),
        h('tbody', null, others.map((from) => h('tr', null, h('th', null, from.name, from.online ? '' : h('span', { class: 'muted small' }, ' · не в сети')),
          targets.map((to) => {
            if (to.id === from.id) return h('td', { class: 'center muted' }, '—');
            const cb = h('input', { type: 'checkbox', checked: (grants[from.id] || []).includes(to.id), title: `${from.name} может управлять ${to.name}` });
            cb.addEventListener('change', async () => {
              if (cb.checked && to.self && !await confirmDlg('Разрешить управлять главным ПК?',
                `С ПК «${from.name}» можно будет открыть рабочий стол, консоль и процессы главного ПК. Сам раздел администратора RANDSEC ему недоступен, но через рабочий стол он увидит всё, что открыто на главном ПК (в том числе открытую панель RANDSEC).`, 'Разрешить', 'danger')) {
                cb.checked = false;
                return;
              }
              const list = new Set(grants[from.id] || []);
              cb.checked ? list.add(to.id) : list.delete(to.id);
              try {
                await save({ grants: { [from.id]: [...list] } });
                grants[from.id] = [...list];
                toast(cb.checked ? `${from.name} может управлять ${to.name}` : `${from.name} больше не может управлять ${to.name}`, 'ok');
              } catch (e) { cb.checked = !cb.checked; toast(e.message, 'bad'); }
            });
            return h('td', { class: 'center' }, cb);
          })))));
      box.replaceChildren(clipCard, h('div', { class: 'card', style: { marginTop: '14px' } }, h('h3', null, 'Кто кем управляет'),
        h('div', { class: 'table-wrap' }, table),
        h('p', { class: 'muted small', style: { marginTop: '10px' } }, 'Разрешённый ПК видит выбранные ПК на своей странице RANDSEC (http://localhost:8420) и может открыть их рабочий стол, консоль и т. д. Главный ПК управляет всеми всегда. Панель администратора (настройки, разрешения, отключение ввода) доступна только на главном ПК.')));
    };
    load().catch((e) => toast(e.message, 'bad'));
    return null;
  }

  // поле кода 2FA для подтверждения важных действий
  const codeField = (label = 'Код из приложения (2FA)') => {
    const inp = h('input', { type: 'text', autocomplete: 'one-time-code', inputmode: 'numeric', placeholder: '123 456' });
    return { inp, el: h('label', { class: 'field' }, h('span', null, label), inp) };
  };

  function showRecovery(codes) {
    const text = codes.join('\n');
    return modal('Резервные коды', [
      h('p', null, 'Сохраните эти коды в надёжном месте (менеджер паролей, бумага). Каждый код одноразовый и нужен, если телефон потерян. Больше они показаны не будут.'),
      h('pre', { class: 'recovery' }, text),
      h('button', { class: 'btn sm', type: 'button', onclick: () => copyText(text) }, 'Копировать'),
    ], [{ label: 'Я сохранил коды', kind: 'primary', submit: true, value: true }]);
  }

  async function enable2FA(done) {
    let r;
    try { r = await api('POST', '/api/2fa/setup'); } catch (e) { toast(e.message, 'bad'); return; }
    const code = codeField('Код из приложения');
    const secret = h('code', { class: 'mono secret' }, r.secret.replace(/(.{4})/g, '$1 ').trim());
    let codes = null;
    for (;;) {
      const v = await modal('Включить двухфакторную аутентификацию', [
        h('ol', { class: 'steps' },
          h('li', null, 'Установите на телефон приложение-аутентификатор: Google Authenticator, Microsoft Authenticator, Яндекс Ключ, Aegis и т. п.'),
          h('li', null, 'Отсканируйте QR-код или введите ключ вручную:',
            h('div', { class: 'qr-box' },
              r.qr ? h('img', { src: r.qr, alt: 'QR-код для приложения-аутентификатора', width: '200', height: '200' }) : null,
              h('div', null, secret, h('div', { style: { marginTop: '8px' } }, h('button', { class: 'btn sm', type: 'button', onclick: () => copyText(r.secret) }, 'Копировать ключ'))))),
          h('li', null, 'Введите 6-значный код, который покажет приложение:', code.el)),
      ], [{ label: 'Отмена', value: null }, { label: 'Включить', kind: 'primary', submit: true, value: 'go' }]);
      if (v !== 'go') break;
      try {
        codes = (await api('POST', '/api/2fa/enable', { code: code.inp.value })).recovery;
        break;
      } catch (e) {
        toast(e.message, 'bad');
        code.inp.value = '';
        if (e.status !== 400 || /истекло/.test(e.message)) break;
      }
    }
    if (codes) {
      toast('Двухфакторная аутентификация включена', 'ok');
      await showRecovery(codes);
    }
    done();
  }

  function confirmWithPassword(title, text, okLabel, kind, need2fa) {
    const pass = h('input', { type: 'password', autocomplete: 'current-password' });
    const code = codeField();
    return modal(title, [h('p', { style: { marginBottom: '12px' } }, text),
      h('label', { class: 'field' }, h('span', null, 'Ваш пароль'), pass), need2fa ? code.el : null],
      [{ label: 'Отмена', value: null }, { label: okLabel, kind, submit: true, getValue: () => ({ password: pass.value, code: code.inp.value }) }]);
  }

  function pageSettings(view) {
    const keyCode = h('code', null, '••••-••••-••••-••••');
    let key = '', shown = false;
    const showBtn = h('button', { class: 'btn sm', onclick: () => { shown = !shown; keyCode.textContent = shown ? key : mask(key); showBtn.textContent = shown ? 'Скрыть' : 'Показать'; } }, 'Показать');
    const mask = (k) => k.replace(/[A-Z0-9](?=.*-)/g, '•').replace(/-[A-Z0-9]{4}$/, '-••••');
    const loadKey = async () => { const r = await api('GET', '/api/enroll-key'); key = r.key; keyCode.textContent = shown ? key : mask(key); };

    const pOld = h('input', { type: 'password', autocomplete: 'current-password' });
    const pNew = h('input', { type: 'password', autocomplete: 'new-password' });
    const pNew2 = h('input', { type: 'password', autocomplete: 'new-password' });
    const pErr = h('div', { class: 'form-err' });

    const tfaCard = h('div', { class: 'card' }, h('h3', null, 'Двухфакторная аутентификация (2FA)'));
    const usersCard = h('div', { class: 'card' }, h('h3', null, 'Пользователи'));
    const netCard = h('div', { class: 'card' }, h('h3', null, 'Доступ из интернета'));
    const ztCard = h('div', { class: 'card' }, h('h3', null, 'Сеть через интернет (ZeroTier)'));
    const indCard = h('div', { class: 'card' }, h('h3', null, 'Плашка на управляемом ПК'));
    const loadInd = async () => {
      let cur;
      try { cur = (await api('GET', '/api/indicator')).pos; } catch (e) { if (e.status === 404) { indCard.remove(); return; } throw e; }
      const POS = [['top-left', 'Вверху слева'], ['top', 'Вверху по центру'], ['top-right', 'Вверху справа'],
        ['bottom-left', 'Внизу слева'], ['bottom', 'Внизу по центру'], ['bottom-right', 'Внизу справа']];
      const screen = h('div', { class: 'ind-screen' + (cur === 'off' ? ' off' : '') });
      const pill = h('div', { class: 'ind-pill' }, h('i'), 'Удалённое управление');
      const place = (p) => { screen.dataset.pos = p; };
      const save = async (p) => {
        try {
          await api('POST', '/api/indicator', { pos: p });
          cur = p;
          screen.classList.toggle('off', p === 'off');
          if (p !== 'off') place(p);
          buttons.forEach((b) => b.classList.toggle('active', b.dataset.pos === p));
          toast(p === 'off' ? 'Плашка больше не показывается' : 'Положение плашки сохранено', 'ok');
        } catch (e) { toast(e.message, 'bad'); }
      };
      // экран-превью: щелчок по месту выбирает положение
      const spots = POS.map(([p, label]) => h('button', { type: 'button', class: 'ind-spot', 'data-pos': p, title: label, onclick: () => save(p) }));
      screen.append(...spots, pill);
      place(cur === 'off' ? 'top' : cur);
      const buttons = [...POS.map(([p, label]) => h('button', { type: 'button', class: 'btn sm' + (p === cur ? ' active' : ''), 'data-pos': p, onclick: () => save(p) }, label)),
        h('button', { type: 'button', class: 'btn sm danger-ghost' + (cur === 'off' ? ' active' : ''), 'data-pos': 'off', onclick: () => save('off') }, 'Не показывать')];
      indCard.replaceChildren(h('h3', null, 'Плашка на управляемом ПК'),
        h('p', { class: 'muted' }, 'Маленькая полупрозрачная плашка «Удалённое управление» на экране ПК, пока к нему подключён удалённый рабочий стол. В саму трансляцию она не попадает. Выберите место на экране или отключите её.'),
        h('div', { class: 'ind-wrap' }, screen, h('div', { class: 'ind-buttons' }, buttons)),
        h('p', { class: 'muted small', style: { marginTop: '10px' } }, 'Предупреждение «администратор отключил клавиатуру и мышь» показывается всегда — чтобы человек за ПК понимал, что происходит.'));
    };
    let ztTimer = null;
    const loadZT = async () => {
      clearTimeout(ztTimer);
      let z;
      try { z = await api('GET', '/api/zerotier'); } catch (e) { if (e.status === 404) { ztCard.remove(); return; } throw e; }
      const state = !z.enabled ? h('span', { class: 'muted' }, 'выключена')
        : z.error ? h('span', { class: 'bad-text' }, z.error)
        : z.status === 'OK' ? h('span', { class: 'ok-text' }, `работает · ПК в сети: ${z.members}`)
        : h('span', { class: 'muted' }, z.step || 'настройка…');
      if (z.enabled && (z.status !== 'OK' || z.step)) ztTimer = setTimeout(() => loadZT().catch(() => {}), 4000);
      ztCard.replaceChildren(h('h3', null, 'Сеть через интернет (ZeroTier)'),
        toggle('Соединять ПК через интернет в любой сети', 'Дома, на работе, через мобильный интернет — без адресов, проброса портов и аккаунтов. Бесплатно и без ограничений на число ПК.',
          z.enabled, async (on) => {
            if (!on && !await confirmDlg('Выключить сеть через интернет?', 'ПК, подключённые только через интернет, потеряют связь с этим ПК.', 'Выключить', 'danger')) { loadZT(); return; }
            await api('POST', '/api/zerotier', { enabled: on });
            toast(on ? 'Включаю сеть через интернет…' : 'Сеть через интернет выключена', 'ok');
            ztTimer = setTimeout(() => loadZT().catch(() => {}), 1500);
          }),
        h('div', { class: 'kv', style: { marginTop: '14px' } },
          h('span', { class: 'k' }, 'Состояние'), state,
          z.network ? h('span', { class: 'k' }, 'Сеть') : '', z.network ? h('span', { class: 'mono small' }, z.network) : '',
          z.addresses && z.addresses.length ? h('span', { class: 'k' }, 'Адрес этого ПК') : '', z.addresses && z.addresses.length ? h('span', { class: 'mono small' }, z.addresses.join(', ')) : ''),
        h('p', { class: 'muted small', style: { marginTop: '10px' } }, 'Как это работает: RANDSEC сам ставит ZeroTier на все ПК, а этот ПК создаёт свою закрытую сеть и управляет ею — аккаунт на сайте ZeroTier не нужен. Номер сети передаётся остальным ПК в зашифрованном виде через общедоступный сервис ntfy.sh; прочитать его и попроситься в сеть может только ПК с тем же логином и паролем.'));
    };

    const isMe = (n) => n.toLowerCase() === (state.user || '').toLowerCase();
    let me = null;

    const loadMe = async () => {
      me = await api('GET', '/api/me');
      const actions = me.totp
        ? [h('button', { class: 'btn sm', onclick: async () => {
            const v = await confirmWithPassword('Новые резервные коды', 'Старые резервные коды перестанут работать.', 'Создать', 'primary', true);
            if (!v) return;
            try { const r = await api('POST', '/api/2fa/recovery', v); await showRecovery(r.recovery); loadMe(); } catch (e) { toast(e.message, 'bad'); }
          } }, 'Новые резервные коды'),
          h('button', { class: 'btn sm danger-ghost', onclick: async () => {
            const v = await confirmWithPassword('Выключить 2FA?', 'Для входа снова будет достаточно только пароля.', 'Выключить', 'danger', true);
            if (!v) return;
            try { await api('POST', '/api/2fa/disable', v); toast('Двухфакторная аутентификация выключена', 'ok'); reloadAll(); } catch (e) { toast(e.message, 'bad'); }
          } }, 'Выключить')]
        : [h('button', { class: 'btn sm primary', onclick: () => enable2FA(reloadAll) }, 'Включить 2FA')];
      tfaCard.replaceChildren(h('h3', null, 'Двухфакторная аутентификация (2FA)'),
        h('p', { class: 'muted' }, 'Кроме пароля при входе понадобится код из приложения на телефоне. Обязательно включите, если хаб доступен из интернета.'),
        h('div', { class: 'kv', style: { marginBottom: '12px' } },
          h('span', { class: 'k' }, 'Состояние'), h('span', { class: me.totp ? 'ok-text' : 'bad-text' }, me.totp ? 'включена' : 'выключена'),
          me.totp ? h('span', { class: 'k' }, 'Резервных кодов') : null, me.totp ? h('span', null, String(me.recovery_left)) : null),
        h('div', { class: 'key-box' }, actions));
    };

    const addUser = async () => {
      const name = h('input', { type: 'text', autocomplete: 'off' });
      const p1 = h('input', { type: 'password', autocomplete: 'new-password' });
      const p2 = h('input', { type: 'password', autocomplete: 'new-password' });
      const cur = h('input', { type: 'password', autocomplete: 'current-password' });
      const code = codeField('Ваш код 2FA');
      const v = await modal('Новый пользователь', [
        h('label', { class: 'field' }, h('span', null, 'Имя пользователя'), name),
        h('label', { class: 'field' }, h('span', null, 'Пароль (от 8 символов)'), p1),
        h('label', { class: 'field' }, h('span', null, 'Повторите пароль'), p2),
        h('label', { class: 'field' }, h('span', null, 'Ваш текущий пароль (подтверждение)'), cur),
        me && me.totp ? code.el : null,
        h('p', { class: 'muted small' }, 'Новый пользователь сможет сам включить 2FA в настройках после первого входа.'),
      ], [{ label: 'Отмена', value: null }, { label: 'Создать', kind: 'primary', submit: true, value: true }]);
      if (!v) return;
      if (p1.value !== p2.value) { toast('Пароли не совпадают', 'bad'); return; }
      try {
        await api('POST', '/api/users', { name: name.value, password: p1.value, current_password: cur.value, code: code.inp.value });
        toast('Пользователь создан', 'ok');
        loadUsers();
      } catch (e) { toast(e.message, 'bad'); }
    };

    const loadUsers = async () => {
      const users = await api('GET', '/api/users');
      usersCard.replaceChildren(h('h3', null, 'Пользователи'),
        h('p', { class: 'muted' }, 'У каждого пользователя свой логин, пароль и своя 2FA. Все пользователи имеют полный доступ.'),
        h('div', { class: 'user-list' }, users.map((u) => h('div', { class: 'user-row' },
          h('span', { class: 'ell' }, u.name, isMe(u.name) ? h('span', { class: 'muted small' }, ' (вы)') : null),
          h('span', { class: 'badge ' + (u.totp ? 'lan' : '') }, u.totp ? '2FA' : 'без 2FA'),
          h('span', { class: 'muted small' }, u.created && !u.created.startsWith('0001') ? fmtTime(u.created) : ''),
          isMe(u.name) ? h('span') : h('button', { class: 'btn sm danger-ghost', onclick: async () => {
            if (!await confirmDlg('Удалить пользователя?', `Пользователь «${u.name}» потеряет доступ к хабу.`, 'Удалить', 'danger')) return;
            try { await api('DELETE', '/api/users/' + enc(u.name)); toast('Пользователь удалён', 'ok'); loadUsers(); } catch (e) { toast(e.message, 'bad'); }
          } }, 'Удалить')))),
        h('button', { class: 'btn sm primary', style: { marginTop: '12px' }, onclick: addUser }, '+ Добавить пользователя'));
    };

    let netTimer = null;
    const loadNet = async () => {
      clearTimeout(netTimer);
      const r = await api('GET', '/api/internet');
      const st = r.status;
      const upnp = h('input', { type: 'checkbox', checked: st.upnp });
      const host = h('input', { type: 'text', value: st.public_host || '', placeholder: 'например myhome.duckdns.org или 203.0.113.5:8420' });
      const save = async () => {
        try {
          await api('POST', '/api/internet', { upnp: upnp.checked, public_host: host.value });
          toast('Сохранено', 'ok');
          netTimer = setTimeout(() => loadNet().catch(() => {}), upnp.checked ? 5000 : 500);
        } catch (e) { toast(e.message, 'bad'); }
      };
      let stLine;
      if (!st.upnp) stLine = h('span', { class: 'muted' }, 'выключен');
      else if (st.active && st.cgnat) stLine = h('span', { class: 'bad-text' }, `порт проброшен, но внешний адрес роутера ${st.external_ip} «серый» (CGNAT): из интернета хаб недоступен. Подключите VPN (Tailscale, ZeroTier) или закажите у провайдера «белый» IP.`);
      else if (st.active) stLine = h('span', { class: 'ok-text' }, `порт ${st.port} проброшен на роутере ${st.gateway}, внешний адрес ${st.external_ip || '?'}`);
      else if (st.error) stLine = h('span', { class: 'bad-text' }, st.error);
      else {
        stLine = h('span', { class: 'muted' }, 'настройка…');
        netTimer = setTimeout(() => loadNet().catch(() => {}), 3000);
      }
      netCard.replaceChildren(h('h3', null, 'Доступ из интернета'),
        h('p', { class: 'muted' }, 'Чтобы ПК подключались к хабу не только из локальной сети. Через интернет соединение всегда шифруется (HTTPS), а агенты проверяют отпечаток сертификата хаба.'),
        !r.totp && (st.upnp || st.public_host) ? h('div', { class: 'warn-box' }, 'Хаб доступен из интернета, а 2FA у вас выключена — включите её выше.') : '',
        h('label', { class: 'pick', style: { marginBottom: '12px' } }, upnp, h('span', null, 'Автоматически пробросить порт на роутере (UPnP)')),
        h('label', { class: 'field' }, h('span', null, 'Свой внешний адрес: домен DDNS или IP (необязательно; нужен, если IP меняется или порт проброшен на роутере вручную)'), host),
        h('button', { class: 'btn primary sm', onclick: save }, 'Сохранить'),
        h('div', { class: 'kv', style: { marginTop: '14px' } },
          h('span', { class: 'k' }, 'UPnP'), stLine,
          h('span', { class: 'k' }, 'Адреса хаба'), h('div', { class: 'addr-list' }, r.addrs.map((a) =>
            h('div', null, h('span', { class: 'mono small' }, a.url), ' ', h('span', { class: 'badge ' + a.kind }, KIND[a.kind] || a.kind)))),
          r.fingerprint ? h('span', { class: 'k' }, 'Отпечаток сертификата') : null,
          r.fingerprint ? h('span', { class: 'mono small break' }, r.fingerprint) : null),
        h('p', { class: 'muted small', style: { marginTop: '12px' } }, 'Агенты, установленные с параметром -fp, получат новые адреса сами при следующем подключении. При открытии https:// в браузере появится предупреждение о самоподписанном сертификате: это ожидаемо, продолжите.'));
    };

    const reloadAll = () => {
      loadMe().catch((e) => toast(e.message, 'bad'));
      loadUsers().catch((e) => toast(e.message, 'bad'));
      loadNet().catch((e) => toast(e.message, 'bad'));
      loadZT().catch((e) => toast(e.message, 'bad'));
      loadInd().catch((e) => toast(e.message, 'bad'));
    };

    view.append(h('div', { class: 'page-head' }, h('h1', null, 'Настройки')),
      h('div', { class: 'settings' },
        tfaCard,
        h('form', { class: 'card', onsubmit: async (e) => {
          e.preventDefault(); pErr.textContent = '';
          if (pNew.value !== pNew2.value) { pErr.textContent = 'Новые пароли не совпадают'; return; }
          try { await api('POST', '/api/password', { old: pOld.value, new: pNew.value }); toast('Пароль изменён', 'ok'); e.target.reset(); } catch (ex) { pErr.textContent = ex.message; }
        } }, h('h3', null, 'Смена пароля'),
          h('label', { class: 'field' }, h('span', null, 'Текущий пароль'), pOld),
          h('label', { class: 'field' }, h('span', null, 'Новый пароль (от 8 символов)'), pNew),
          h('label', { class: 'field' }, h('span', null, 'Повторите новый пароль'), pNew2),
          pErr, h('button', { class: 'btn primary', type: 'submit' }, 'Сменить пароль')),
        usersCard,
        ztCard,
        indCard,
        netCard,
        h('div', { class: 'card' }, h('h3', null, 'Ключ привязки ПК'),
          h('p', { class: 'muted' }, 'По этому ключу агенты привязываются к вашему хабу. После смены ключа уже привязанные ПК продолжат работать, а новые нужно будет привязывать новым ключом.'),
          h('div', { class: 'key-box' }, keyCode, showBtn,
            h('button', { class: 'btn sm', onclick: () => copyText(key) }, 'Копировать'),
            h('button', { class: 'btn sm', onclick: showAddDevice }, 'Инструкция'),
            h('button', { class: 'btn sm danger-ghost', onclick: async () => {
              if (!await confirmDlg('Сменить ключ?', 'Старый ключ перестанет работать для привязки новых ПК.', 'Сменить', 'danger')) return;
              try { const r = await api('POST', '/api/enroll-key/rotate'); key = r.key; keyCode.textContent = shown ? key : mask(key); toast('Ключ изменён', 'ok'); } catch (e) { toast(e.message, 'bad'); }
            } }, 'Сменить ключ')))));
    loadKey().catch((e) => toast(e.message, 'bad'));
    reloadAll();
    return () => { clearTimeout(netTimer); clearTimeout(ztTimer); };
  }

  boot();
})();
