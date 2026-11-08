'use strict';

/* ============ Utilidades ============ */

const $ = (s) => document.querySelector(s);

function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

// Iconos constantes (nunca contienen datos del usuario).
const ICONS = {
  copy: '<path d="M16 1H4a2 2 0 0 0-2 2v14h2V3h12V1Zm3 4H8a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2Zm0 16H8V7h11v14Z"/>',
  open: '<path d="M14 3v2h3.6l-9.8 9.8 1.4 1.4L19 6.4V10h2V3h-7Zm5 16H5V5h7V3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7h-2v7Z"/>',
  download: '<path d="M5 20h14v-2H5v2Zm14-9h-4V3H9v8H5l7 7 7-7Z"/>',
  pin: '<path d="M16 3H8v2h1v6l-2 3v2h4.2v5h1.6v-5H17v-2l-2-3V5h1V3Z"/>',
  trash: '<path d="M6 19a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2V7H6v12ZM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4Z"/>',
  file: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6Zm4 18H6V4h7v5h5v11Z"/>',
  text: '<path d="M4 5h16v2H4V5Zm0 4h16v2H4V9Zm0 4h16v2H4v-2Zm0 4h10v2H4v-2Z"/>',
  expand: '<path d="M4 4h6v2H6v4H4V4Zm10 0h6v6h-2V6h-4V4ZM4 14h2v4h4v2H4v-6Zm14 0h2v6h-6v-2h4v-4Z"/>',
};
function icon(name) {
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  s.setAttribute('viewBox', '0 0 24 24');
  s.setAttribute('aria-hidden', 'true');
  s.innerHTML = ICONS[name];
  return s;
}

function toast(msg, err = false) {
  const t = h('div', { class: 'toast' + (err ? ' err' : '') }, msg);
  $('#toasts').append(t);
  setTimeout(() => t.remove(), err ? 4500 : 2200);
}

function humanSize(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GB';
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB';
  if (n >= 1 << 10) return Math.round(n / (1 << 10)) + ' KB';
  return n + ' B';
}

function timeAgo(iso) {
  const d = new Date(iso);
  const s = (Date.now() - d) / 1000;
  if (s < 45) return 'ahora';
  if (s < 3600) return `hace ${Math.round(s / 60)} min`;
  if (s < 86400) return `hace ${Math.round(s / 3600)} h`;
  if (s < 172800) return 'ayer';
  return d.toLocaleDateString('es-ES', { day: 'numeric', month: 'short' });
}

function fmtDate(iso) {
  const d = new Date(iso);
  if (d.getFullYear() < 2000) return 'nunca';
  return d.toLocaleString('es-ES', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const ta = h('textarea');
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    ta.value = text;
    document.body.append(ta);
    ta.select();
    document.execCommand('copy');
    ta.remove();
  }
  toast('Copiado ✓');
}

// "¿Seguro?" en el propio botón, sin diálogos del navegador.
function confirmButton(btn, label, action) {
  let armed = false, timer;
  const original = [...btn.childNodes];
  btn.addEventListener('click', () => {
    if (!armed) {
      armed = true;
      btn.classList.add('confirm');
      btn.replaceChildren(label);
      timer = setTimeout(() => { armed = false; btn.classList.remove('confirm'); btn.replaceChildren(...original); }, 3000);
      return;
    }
    clearTimeout(timer);
    action();
  });
  return btn;
}

/* ============ Portapapeles ============ */

const CLIP_IMAGES = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/gif']);

function isClippable(it) {
  return it.kind === 'text' || (it.kind === 'file' && CLIP_IMAGES.has(it.mime) && it.size < 50 * 1024 * 1024);
}

async function toPng(blob) {
  if (blob.type === 'image/png') return blob;
  const bmp = await createImageBitmap(blob);
  const c = document.createElement('canvas');
  c.width = bmp.width;
  c.height = bmp.height;
  c.getContext('2d').drawImage(bmp, 0, 0);
  return new Promise((ok, ko) => c.toBlob((b) => (b ? ok(b) : ko(new Error('No se pudo convertir la imagen'))), 'image/png'));
}

// Lanza un error si el navegador no lo permite (p. ej. Safari sin un toque).
async function writeClipboard(it) {
  if (it.kind === 'text') return navigator.clipboard.writeText(it.text);
  // La promesa va dentro del ClipboardItem para que Safari lo acepte dentro del toque.
  const png = fetch(it.url).then((r) => r.blob()).then(toPng);
  return navigator.clipboard.write([new ClipboardItem({ 'image/png': png })]);
}

async function copyItem(it) {
  try {
    if (it.kind === 'text') return copyText(it.text);
    await writeClipboard(it);
    toast('Imagen copiada ✓');
  } catch (e) {
    toast('No se pudo copiar: ' + (e.message || e), true);
  }
}

let clipTimer;
function showClipBar(it) {
  $('#clipbar-title').textContent = (it.kind === 'text' ? '📋 Texto' : '🖼️ Imagen') + ' de ' + it.from;
  $('#clipbar-preview').textContent = it.kind === 'text' ? it.text.replace(/\s+/g, ' ').slice(0, 140) : it.name;
  $('#clipbar-copy').onclick = async () => { hideClipBar(); await copyItem(it); };
  $('#clipbar').hidden = false;
  clearTimeout(clipTimer);
  clipTimer = setTimeout(hideClipBar, 60 * 1000);
}
function hideClipBar() { $('#clipbar').hidden = true; }
$('#clipbar-close').addEventListener('click', hideClipBar);

// Algo nuevo ha llegado desde otro dispositivo: intentamos copiarlo solo;
// si el navegador no deja (o la app acaba de abrirse), mostramos el aviso.
async function offerClip(it, justOpened) {
  if (!prefs.autocopy) return;
  if (!justOpened && document.visibilityState === 'visible' && document.hasFocus()) {
    try {
      await writeClipboard(it);
      toast((it.kind === 'text' ? '📋 Texto' : '🖼️ Imagen') + ' de ' + it.from + ' copiado');
      return;
    } catch {}
  }
  showClipBar(it);
}

/* ============ API ============ */

class AuthError extends Error {}

async function api(method, path, body) {
  const opts = { method, headers: { 'X-Cofre-Touch': navigator.maxTouchPoints > 1 ? '1' : '0' }, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = typeof body === 'string' ? body : JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  let data = null;
  try { data = await res.json(); } catch {}
  if (res.status === 401 && !path.startsWith('/api/login') && !path.startsWith('/api/enroll')) {
    showScreen('login');
    throw new AuthError(data?.error || 'Sesión caducada');
  }
  if (!res.ok) throw new Error(data?.error || `Error ${res.status}`);
  return data;
}

/* ============ WebAuthn ============ */

const b64u = {
  dec(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    const bin = atob(s + '='.repeat((4 - (s.length % 4)) % 4));
    return Uint8Array.from(bin, (c) => c.charCodeAt(0)).buffer;
  },
  enc(buf) {
    const bytes = new Uint8Array(buf);
    let bin = '';
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  },
};

function webauthnError(e) {
  if (e?.name === 'NotAllowedError') return 'Cancelado o se agotó el tiempo. Inténtalo de nuevo.';
  if (e?.name === 'InvalidStateError') return 'Esa llave ya está registrada.';
  if (e?.name === 'SecurityError') return 'Error de seguridad: abre el Cofre con su nombre (no con la IP) y con el certificado instalado.';
  return e?.message || String(e);
}

// Algunos navegadores (p. ej. la app web de iOS) devuelven extensiones que no
// se pidieron, como "appid"; el servidor las rechaza, así que solo enviamos las pedidas.
function requestedExtensions(cred, pk) {
  const all = cred.getClientExtensionResults() || {};
  const asked = pk.extensions || {};
  return Object.fromEntries(Object.entries(all).filter(([k]) => k in asked));
}

function ensureWebAuthn() {
  if (!window.PublicKeyCredential) {
    throw new Error('Este navegador no permite llaves de seguridad aquí. ¿Instalaste el certificado del Cofre?');
  }
}

async function registerKey(code, name) {
  ensureWebAuthn();
  const opts = await api('POST', '/api/enroll/begin', { code, name });
  const pk = opts.publicKey;
  pk.challenge = b64u.dec(pk.challenge);
  pk.user.id = b64u.dec(pk.user.id);
  (pk.excludeCredentials || []).forEach((c) => (c.id = b64u.dec(c.id)));
  const cred = await navigator.credentials.create({ publicKey: pk });
  await api('POST', '/api/enroll/finish', {
    id: cred.id,
    rawId: b64u.enc(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment || undefined,
    clientExtensionResults: requestedExtensions(cred, pk),
    response: {
      attestationObject: b64u.enc(cred.response.attestationObject),
      clientDataJSON: b64u.enc(cred.response.clientDataJSON),
      transports: cred.response.getTransports ? cred.response.getTransports() : undefined,
    },
  });
}

async function login() {
  ensureWebAuthn();
  const opts = await api('POST', '/api/login/begin');
  const pk = opts.publicKey;
  pk.challenge = b64u.dec(pk.challenge);
  (pk.allowCredentials || []).forEach((c) => (c.id = b64u.dec(c.id)));
  const cred = await navigator.credentials.get({ publicKey: pk });
  await api('POST', '/api/login/finish', {
    id: cred.id,
    rawId: b64u.enc(cred.rawId),
    type: cred.type,
    clientExtensionResults: requestedExtensions(cred, pk),
    response: {
      authenticatorData: b64u.enc(cred.response.authenticatorData),
      clientDataJSON: b64u.enc(cred.response.clientDataJSON),
      signature: b64u.enc(cred.response.signature),
      userHandle: cred.response.userHandle ? b64u.enc(cred.response.userHandle) : null,
    },
  });
}

/* ============ Pantallas ============ */

function showScreen(name) {
  for (const s of ['boot', 'login', 'enroll', 'main']) $('#screen-' + s).hidden = s !== name;
  if (name !== 'main') disconnectEvents();
}

async function boot() {
  try {
    const st = await api('GET', '/api/status');
    if (!st.enrolled) return showScreen('enroll');
    if (!st.authed) return showScreen('login');
    enterMain();
  } catch (e) {
    showScreen('login');
    $('#login-error').textContent = 'No se pudo conectar con el Cofre.';
  }
}

$('#btn-login').addEventListener('click', async () => {
  const btn = $('#btn-login');
  $('#login-error').textContent = '';
  btn.disabled = true;
  try {
    await login();
    enterMain();
  } catch (e) {
    $('#login-error').textContent = webauthnError(e);
  } finally {
    btn.disabled = false;
  }
});

$('#form-enroll').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  $('#enroll-error').textContent = '';
  try {
    await registerKey($('#enroll-code').value, $('#enroll-name').value);
    toast('Llave registrada ✓');
    enterMain();
  } catch (e) {
    // ¿Se registró ya la llave desde otro dispositivo? Entonces toca iniciar sesión.
    const st = await api('GET', '/api/status').catch(() => null);
    if (st?.enrolled) {
      showScreen('login');
      $('#login-error').textContent = 'Tu llave ya está registrada: entra con ella.';
      return;
    }
    $('#enroll-error').textContent = webauthnError(e);
  }
});

/* ============ Lista de elementos ============ */

const prefs = {
  get autocopy() { try { return localStorage.getItem('cofre.autocopy') !== '0'; } catch { return true; } },
  set autocopy(v) { try { localStorage.setItem('cofre.autocopy', v ? '1' : '0'); } catch {} },
};

let items = [];
let maxUpload = Infinity;
let filter = 'all';
let seen = new Set();
let firstLoad = true;

function enterMain() {
  showScreen('main');
  firstLoad = true;
  loadItems();
  connectEvents();
}

async function loadItems() {
  try {
    const data = await api('GET', '/api/items');
    const wasFirst = firstLoad;
    const incoming = data.items.filter((it) => !it.mine && isClippable(it) &&
      (wasFirst ? Date.now() - new Date(it.created) < 2 * 60 * 1000 : !seen.has(it.id)));
    items = data.items;
    if (data.maxUpload) maxUpload = data.maxUpload;
    render();
    if (incoming.length) {
      const newest = incoming.reduce((a, b) => (new Date(a.created) > new Date(b.created) ? a : b));
      offerClip(newest, wasFirst);
    }
  } catch (e) {
    if (!(e instanceof AuthError)) toast(e.message, true);
  }
}

function kindOf(it) {
  const m = it.mime || '';
  if (m === 'application/pdf') return 'pdf';
  if (m.startsWith('image/')) return 'img';
  if (m.startsWith('video/')) return 'vid';
  if (m.startsWith('audio/')) return 'aud';
  if (/zip|rar|7z|tar|gzip|compressed/.test(m)) return 'zip';
  return 'file';
}

const INLINE = new Set(['pdf', 'img', 'vid', 'aud']);

function render() {
  const feed = $('#feed');
  const list = items.filter((it) =>
    filter === 'all' ? true : filter === 'pinned' ? it.pinned : it.kind === filter);
  feed.replaceChildren(...list.map((it) => {
    const fresh = !firstLoad && !seen.has(it.id);
    return card(it, fresh);
  }));
  items.forEach((it) => seen.add(it.id));
  firstLoad = false;
  $('#empty').hidden = list.length > 0;
  $('#empty p').textContent = items.length === 0
    ? 'Tu cofre está vacío. Sube algo y aparecerá en todos tus dispositivos.'
    : 'No hay nada con este filtro.';
}

function metaLine(it) {
  return h('div', { class: 'meta' },
    it.pinned ? h('span', { class: 'pin-badge' }, '📌') : null,
    it.kind === 'file' ? h('span', {}, humanSize(it.size)) : null,
    it.kind === 'file' ? h('span', {}, '·') : null,
    h('span', {}, it.from),
    h('span', {}, '·'),
    h('span', { title: new Date(it.created).toLocaleString('es-ES') }, timeAgo(it.created)),
  );
}

function commonActions(it) {
  const pinBtn = h('button', { class: 'btn ghost' + (it.pinned ? ' on' : ''), title: it.pinned ? 'Desfijar' : 'Fijar (no se borra solo)',
    'aria-label': it.pinned ? 'Desfijar' : 'Fijar', onclick: async () => {
    try { await api('POST', `/api/items/${it.id}/pin`, { pinned: !it.pinned }); } catch (e) { toast(e.message, true); }
  } }, icon('pin'));
  const delBtn = confirmButton(h('button', { class: 'btn ghost', title: 'Borrar', 'aria-label': 'Borrar' }, icon('trash')), '¿Borrar?', async () => {
    try { await api('DELETE', `/api/items/${it.id}`); } catch (e) { toast(e.message, true); }
  });
  return [h('span', { class: 'spacer' }), pinBtn, delBtn];
}

function card(it, fresh) {
  if (it.kind === 'text') {
    const long = it.text.length > 400 || it.text.split('\n').length > 6;
    return h('li', { class: 'card' + (fresh ? ' fresh' : '') },
      h('div', { class: 'card-main' },
        h('div', { class: 'thumb' }, icon('text')),
        h('div', { class: 'info' }, h('div', { class: 'name' }, 'Texto'), metaLine(it))),
      h('pre', { class: 'text-body' + (long ? '' : ' short') }, it.text),
      h('div', { class: 'actions' },
        h('button', { class: 'btn primary', onclick: () => copyText(it.text) }, icon('copy'), 'Copiar'),
        long ? h('button', { class: 'btn ghost', onclick: () => openText(it.text) }, icon('expand'), 'Ver todo') : null,
        ...commonActions(it)));
  }

  const k = kindOf(it);
  const label = (it.name.split('.').pop() || '').slice(0, 4).toUpperCase();
  const thumb = h('div', { class: 'thumb ' + k }, k === 'file' || k === 'aud' ? icon('file') : label || icon('file'));
  const showImg = k === 'img' && it.size < 15 * 1024 * 1024 && !/heic/.test(it.mime);
  let preview = null;
  if (showImg) {
    preview = h('img', { class: 'img-preview', src: it.url, alt: it.name, loading: 'lazy',
      onclick: () => window.open(it.url, '_blank', 'noopener') });
  }
  return h('li', { class: 'card' + (fresh ? ' fresh' : '') },
    h('div', { class: 'card-main' },
      thumb,
      h('div', { class: 'info' }, h('div', { class: 'name', title: it.name }, it.name), metaLine(it))),
    preview,
    h('div', { class: 'actions' },
      INLINE.has(k) ? h('a', { class: 'btn primary', href: it.url, target: '_blank', rel: 'noopener' }, icon('open'), 'Abrir') : null,
      h('a', { class: 'btn ' + (INLINE.has(k) ? 'ghost' : 'primary'), href: it.dl, download: it.name, title: 'Guardar', 'aria-label': 'Guardar' },
        icon('download'), h('span', { class: 'lbl' }, 'Guardar')),
      isClippable(it) ? h('button', { class: 'btn ghost', title: 'Copiar imagen', 'aria-label': 'Copiar imagen', onclick: () => copyItem(it) }, icon('copy')) : null,
      ...commonActions(it)));
}

function openText(text) {
  $('#text-full').textContent = text;
  $('#btn-copy-full').onclick = () => copyText(text);
  $('#dlg-text').showModal();
}

$('#filters').addEventListener('click', (e) => {
  const b = e.target.closest('.chip');
  if (!b) return;
  filter = b.dataset.f;
  document.querySelectorAll('.chip').forEach((c) => c.classList.toggle('active', c === b));
  render();
});

/* ============ Enviar texto ============ */

const ta = $('#text');
const sendBtn = $('#btn-send');

function autoGrow() {
  ta.style.height = 'auto';
  ta.style.height = Math.min(ta.scrollHeight, window.innerHeight * 0.4) + 'px';
  sendBtn.disabled = ta.value.trim() === '';
}
ta.addEventListener('input', autoGrow);
ta.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); sendText(); }
});
sendBtn.addEventListener('click', sendText);

async function sendText() {
  const text = ta.value;
  if (!text.trim()) return;
  sendBtn.disabled = true;
  try {
    await api('POST', '/api/text', { text });
    ta.value = '';
    autoGrow();
    toast('Texto enviado ✓');
  } catch (e) {
    toast(e.message, true);
    sendBtn.disabled = false;
  }
}

/* ============ Subir archivos ============ */

$('#file-input').addEventListener('change', (e) => {
  if (e.target.files.length) uploadFiles([...e.target.files]);
  e.target.value = '';
});

function uploadFiles(files) {
  const total = files.reduce((a, f) => a + f.size, 0);
  if (total > maxUpload) {
    toast(`Demasiado grande (${humanSize(total)}). El máximo desde el navegador es ${humanSize(maxUpload)}; desde el portátil usa "cofre enviar".`, true);
    return;
  }
  const label = files.length === 1 ? files[0].name : `${files.length} archivos`;
  const bar = h('i');
  const pct = h('span', {}, '0%');
  const row = h('div', { class: 'upload' },
    h('div', { class: 'meta' }, h('strong', {}, label), h('span', {}, '· ' + humanSize(total) + ' ·'), pct),
    h('div', { class: 'bar' }, bar));
  $('#uploads').append(row);

  const fd = new FormData();
  files.forEach((f) => fd.append('file', f, f.name));
  const xhr = new XMLHttpRequest();
  xhr.open('POST', '/api/upload');
  xhr.upload.onprogress = (e) => {
    if (!e.lengthComputable) return;
    const p = Math.round((e.loaded / e.total) * 100);
    bar.style.width = p + '%';
    pct.textContent = p < 100 ? p + '%' : 'cifrando…';
  };
  xhr.onload = () => {
    row.remove();
    if (xhr.status === 200) toast(files.length === 1 ? 'Subido ✓' : `${files.length} archivos subidos ✓`);
    else if (xhr.status === 401) showScreen('login');
    else {
      let msg = 'Error al subir';
      try { msg = JSON.parse(xhr.responseText).error || msg; } catch {}
      toast(msg, true);
    }
  };
  xhr.onerror = () => { row.remove(); toast('Se perdió la conexión durante la subida', true); };
  xhr.send(fd);
}

// Arrastrar y soltar
let dragDepth = 0;
const hasFiles = (e) => [...(e.dataTransfer?.types || [])].includes('Files');
window.addEventListener('dragenter', (e) => {
  if (!hasFiles(e) || $('#screen-main').hidden) return;
  e.preventDefault();
  dragDepth++;
  $('#dropzone').hidden = false;
});
window.addEventListener('dragleave', () => {
  if (--dragDepth <= 0) { dragDepth = 0; $('#dropzone').hidden = true; }
});
window.addEventListener('dragover', (e) => { if (hasFiles(e)) e.preventDefault(); });
window.addEventListener('drop', (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault();
  dragDepth = 0;
  $('#dropzone').hidden = true;
  if (!$('#screen-main').hidden && e.dataTransfer.files.length) uploadFiles([...e.dataTransfer.files]);
});

// Pegar archivos/capturas con Ctrl+V
document.addEventListener('paste', (e) => {
  if ($('#screen-main').hidden) return;
  const files = [...(e.clipboardData?.files || [])];
  if (files.length) {
    e.preventDefault();
    uploadFiles(files.map((f, i) => f.name && f.name !== 'image.png'
      ? f : new File([f], `captura-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}${i ? '-' + i : ''}.png`, { type: f.type })));
  }
});

/* ============ Tiempo real ============ */

let es = null;

function connectEvents() {
  disconnectEvents();
  es = new EventSource('/api/events');
  es.onopen = () => $('#live').classList.add('on');
  es.onerror = () => {
    $('#live').classList.remove('on');
    if (es && es.readyState === EventSource.CLOSED) {
      // Puede ser sesión caducada: lo comprobamos.
      setTimeout(() => api('GET', '/api/status').then((s) => {
        if (!s.authed) showScreen('login');
        else if (!$('#screen-main').hidden) connectEvents();
      }).catch(() => {}), 3000);
    }
  };
  es.addEventListener('items', loadItems);
  es.addEventListener('account', () => { if ($('#dlg-settings').open) loadAccount(); });
  es.addEventListener('logout', () => { showScreen('login'); });
}

function disconnectEvents() {
  if (es) { es.close(); es = null; }
  $('#live')?.classList.remove('on');
}

// Al volver a la app (móvil bloqueado, cambio de pestaña…) refrescamos.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible' && !$('#screen-enroll').hidden) { boot(); return; }
  if (document.visibilityState !== 'visible' || $('#screen-main').hidden) return;
  loadItems();
  if (!es || es.readyState === EventSource.CLOSED) connectEvents();
});
// Los enlaces de descarga caducan a los 15 min: los renovamos.
setInterval(() => {
  if (document.visibilityState === 'visible' && !$('#screen-main').hidden) loadItems();
}, 10 * 60 * 1000);
// Actualiza los "hace X min".
setInterval(() => { if (!$('#screen-main').hidden) render(); }, 60 * 1000);

/* ============ Ajustes ============ */

document.querySelectorAll('dialog [data-close]').forEach((b) =>
  b.addEventListener('click', () => b.closest('dialog').close()));
document.querySelectorAll('dialog').forEach((d) =>
  d.addEventListener('click', (e) => { if (e.target === d) d.close(); }));

$('#btn-settings').addEventListener('click', async () => {
  $('#dlg-settings').showModal();
  loadAccount();
});

async function loadAccount() {
  let acc;
  try { acc = await api('GET', '/api/account'); } catch (e) { return; }

  $('#sessions').replaceChildren(...acc.sessions.map((s) => h('li', {},
    h('div', { class: 'grow' },
      h('div', {}, s.Device, ' ', s.Current ? h('span', { class: 'tag' }, 'ESTE') : null),
      h('div', { class: 'sub' }, `Activo ${timeAgo(s.LastSeen)} · caduca ${fmtDate(s.Expires)}`)),
    confirmButton(h('button', { class: 'btn ghost' }, s.Current ? 'Salir' : 'Expulsar'), '¿Seguro?', async () => {
      await api('DELETE', `/api/sessions/${s.ID}`).catch((e) => toast(e.message, true));
      if (s.Current) showScreen('login'); else loadAccount();
    }))));

  $('#keys').replaceChildren(...acc.keys.map((k) => h('li', {},
    h('div', { class: 'grow' },
      h('div', {}, '🔑 ', k.Name),
      h('div', { class: 'sub' }, `Añadida ${fmtDate(k.Added)} · último uso ${fmtDate(k.LastUsed)}`)),
    acc.keys.length > 1 ? confirmButton(h('button', { class: 'btn ghost' }, icon('trash')), '¿Quitar?', async () => {
      await api('DELETE', `/api/keys/${k.ID}`).catch((e) => toast(e.message, true));
      loadAccount();
    }) : null)));

  $('#set-laptopclip').checked = !acc.settings.clipboardOff;
  $('#set-autocopy').checked = prefs.autocopy;
  $('#set-retention').value = String(acc.settings.retentionDays);
  $('#set-session').value = String(acc.settings.sessionDays);
}

async function saveSettings() {
  try {
    await api('POST', '/api/settings', {
      retentionDays: +$('#set-retention').value,
      sessionDays: +$('#set-session').value,
      clipboardOff: !$('#set-laptopclip').checked,
    });
    toast('Guardado ✓');
  } catch (e) { toast(e.message, true); }
}
$('#set-retention').addEventListener('change', saveSettings);
$('#set-session').addEventListener('change', saveSettings);
$('#set-laptopclip').addEventListener('change', saveSettings);
$('#set-autocopy').addEventListener('change', (e) => {
  prefs.autocopy = e.target.checked;
  toast(e.target.checked ? 'Copia automática activada en este dispositivo' : 'Copia automática desactivada');
});

$('#form-addkey').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    await registerKey('', $('#addkey-name').value || 'Llave de repuesto');
    $('#addkey-name').value = '';
    toast('Llave añadida ✓');
    loadAccount();
  } catch (err) { toast(webauthnError(err), true); }
});

confirmButton($('#btn-clear'), '¿Seguro? Pulsa otra vez', async () => {
  try {
    const r = await api('POST', '/api/clear', { keepPinned: true });
    toast(`${r.deleted} elementos borrados`);
    $('#dlg-settings').close();
  } catch (e) { toast(e.message, true); }
});

$('#btn-logout').addEventListener('click', async () => {
  try { await api('POST', '/api/logout'); } catch {}
  $('#dlg-settings').close();
  showScreen('login');
});

boot();
