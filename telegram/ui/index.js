/* ═══════════════════════════════════════════════════════
   NusaShell Telegram — Frontend (UI 0.2)
   Plain HTML/JS, no build step. Talks to the MCP backend via the
   NusaShell plugin bridge (window.shell.callTool). No mock data.
   ═══════════════════════════════════════════════════════ */

'use strict';

// ── Bridge ─────────────────────────────────────────────
const PLUGIN_ID =
  new URLSearchParams(location.search).get('pluginId') || 'nusashell.telegram';

// callTool unwraps the bridge envelope ({ result: CallToolResult }) and
// surfaces tool-level errors as rejections. Prefers structuredContent;
// falls back to parsing the JSON text payload so callers always get real
// values (e.g. [] for an empty list), never the raw envelope.
async function callTool(tool, args) {
  if (!window.shell || typeof window.shell.callTool !== 'function') {
    throw new Error('NusaShell shell tidak tersedia. Buka plugin ini lewat NusaShell.');
  }
  const envelope = await window.shell.callTool(PLUGIN_ID, tool, args || {});

  let payload = envelope;
  if (payload && typeof payload === 'object' && 'result' in payload) {
    payload = payload.result;
  }

  if (payload && typeof payload === 'object' && 'isError' in payload && payload.isError) {
    const text = (payload.content || [])
      .map((c) => c && (c.text ?? c.Text))
      .filter(Boolean)
      .join('\n');
    throw new Error(text || `Tool ${tool} gagal`);
  }

  if (payload && typeof payload === 'object' && 'structuredContent' in payload) {
    const sc = payload.structuredContent;
    if (sc !== null && sc !== undefined) return sc;
    const text = (payload.content || [])
      .map((c) => c && (c.text ?? c.Text))
      .filter(Boolean)
      .join('\n');
    if (typeof text === 'string') {
      try { return JSON.parse(text); } catch { /* keep envelope */ }
    }
  }

  if (payload && typeof payload === 'object' && 'items' in payload && Array.isArray(payload.items)) {
    return payload.items;
  }

  return payload;
}

function asArray(v) { return Array.isArray(v) ? v : []; }
function asObject(v) { return v && typeof v === 'object' ? v : {}; }

// ── Toasts & confirm ─────────────────────────────────
function showToast(message, type, duration) {
  const container = document.getElementById('toast-container');
  if (!container) return;
  const toast = document.createElement('div');
  toast.className = `toast ${type || 'info'}`;
  const icons = { success: '✓', error: '!', info: 'i', warning: '!' };
  toast.innerHTML =
    `<span class="toast-icon">${icons[type] || 'i'}</span>` +
    `<span class="toast-msg">${escapeHtml(message)}</span>` +
    `<button class="toast-close" aria-label="Close" onclick="this.parentElement.remove()">✕</button>`;
  container.appendChild(toast);
  setTimeout(() => toast.classList.add('fade-out'), (duration || 3500) - 220);
  setTimeout(() => toast.remove(), duration || 3500);
}

let confirmResolve = null;

function showConfirm(title, message, okLabel) {
  return new Promise((resolve) => {
    const overlay = document.getElementById('confirm-dialog');
    if (!overlay) { resolve(false); return; }
    document.getElementById('confirm-dialog-title').textContent = title;
    document.getElementById('confirm-dialog-message').textContent = message;
    const ok = document.getElementById('confirm-dialog-ok');
    ok.textContent = okLabel || 'Confirm';
    overlay.style.display = 'flex';
    confirmResolve = resolve;
  });
}

function handleConfirmOk() {
  const overlay = document.getElementById('confirm-dialog');
  if (overlay) overlay.style.display = 'none';
  if (confirmResolve) { confirmResolve(true); confirmResolve = null; }
}

function handleConfirmCancel() {
  const overlay = document.getElementById('confirm-dialog');
  if (overlay) overlay.style.display = 'none';
  if (confirmResolve) { confirmResolve(false); confirmResolve = null; }
}

// ── State ─────────────────────────────────────────────
let currentChatId = null;
let currentFilter = 'all';
let searchTimer = null;
let lastRenderSig = '';

function parseHash(hash) {
  const h = hash || location.hash;
  const parts = (h || '').replace(/^#\/?/, '').split('/');
  const page = parts[0] || 'status';
  const id = parts.slice(1).join('/') || null;
  return { page, id };
}

function navigate(hash) {
  if (location.hash !== hash) history.pushState(null, '', hash);
  render(hash);
}

async function render(hash) {
  const { page, id } = parseHash(hash);
  document.querySelectorAll('.nav-item').forEach((l) => {
    l.classList.toggle('active', l.dataset.page === page);
  });
  const content = document.getElementById('content');
  if (page === 'chat') {
    currentChatId = id ? decodeURIComponent(id) : currentChatId;
    if (!currentChatId) { location.hash = '#/chats'; return; }
  }
  if (page === 'approvals') updateApprovalBadge(true);

  content.innerHTML = '<div class="loading-overlay"><span class="spinner"></span></div>';
  try {
    switch (page) {
      case 'login': await renderLogin(content); break;
      case 'status': await renderStatus(content); break;
      case 'chats': lastRenderSig = ''; await renderChats(content); break;
      case 'chat': lastRenderSig = ''; await renderChat(content); break;
      case 'approvals': await renderApprovals(content); break;
      case 'settings': await renderSettings(content); break;
      default: location.hash = '#/status';
    }
  } catch (e) {
    content.innerHTML = '';
    showToast(e.message, 'error');
  }
}

// ── Polling ───────────────────────────────────────────
// Sidebar connection chip + approval count (every 30s); the current page is
// refreshed silently (chats list every 10s, open thread every 7s) and only
// re-rendered when the data signature actually changed, so we never flash
// or lose scroll position.
let connTimer = null;
let pageTimer = null;

function startPolling() {
  stopPolling();
  connTimer = setInterval(pollConnection, 30000);
  pageTimer = setInterval(pollCurrentPage, 10000);
  pollConnection();
}

function stopPolling() {
  if (connTimer) { clearInterval(connTimer); connTimer = null; }
  if (pageTimer) { clearInterval(pageTimer); pageTimer = null; }
}

async function pollConnection() {
  if (document.hidden) return;
  try {
    const st = asObject(await callTool('status'));
    updateConnChip(st);
    updateApprovalBadge(false);
    lastStatus = st;
  } catch { /* keep last state */ }
}

let lastStatus = null;

async function pollCurrentPage() {
  if (document.hidden) return;
  const { page } = parseHash();
  if (page !== 'chats' && page !== 'chat') return;
  try {
    if (page === 'chats') await refreshChatsSilently();
    else await refreshThreadSilently();
  } catch { /* transient — ignore */ }
}

// ── Layout helpers ────────────────────────────────────
function updateConnChip(st) {
  const dot = document.getElementById('connDot');
  const label = document.getElementById('connLabel');
  if (!dot || !label) return;
  const s = asObject(st);
  if (s.connected) {
    dot.className = 'conn-dot ok';
    label.textContent = s.bot_name ? `@${String(s.bot_name).replace(/^@/, '')}` : 'Online';
  } else if (s.paired) {
    dot.className = 'conn-dot warn';
    label.textContent = 'Linked · polling down';
  } else {
    dot.className = 'conn-dot bad';
    label.textContent = 'Not connected';
  }
}

async function updateApprovalBadge(force) {
  const badge = document.getElementById('approvalBadge');
  if (!badge) return;
  const { page } = parseHash();
  if (!force && page === 'approvals') return; // page shows its own list
  try {
    const data = asObject(await callTool('list_pending_approvals'));
    const n = asArray(data.approvals).length;
    badge.style.display = n > 0 ? 'inline-flex' : 'none';
    badge.textContent = n > 99 ? '99+' : String(n);
  } catch { /* keep */ }
}

function pageHeader(title, subtitle, actionsHtml) {
  return `
    <div class="page-header">
      <div>
        <div class="page-title">${escapeHtml(title)}</div>
        ${subtitle ? `<div class="page-subtitle">${escapeHtml(subtitle)}</div>` : ''}
      </div>
      ${actionsHtml || ''}
    </div>`;
}

function emptyState(icon, title, desc, card) {
  const inner = `<div class="empty-state">
      <div class="empty-state-icon">${icon}</div>
      <div class="empty-state-title">${escapeHtml(title)}</div>
      ${desc ? `<div class="empty-state-desc">${escapeHtml(desc)}</div>` : ''}
    </div>`;
  return card ? `<div class="empty-card">${inner}</div>` : inner;
}

function avatarHtml(name, type) {
  const initial = String(name || '?').trim().charAt(0).toUpperCase() || '?';
  return `<div class="chat-avatar k-${escapeAttr(type || 'dm')}">${escapeHtml(initial)}</div>`;
}

// ── Formatting ────────────────────────────────────────
function escapeHtml(str) {
  if (str == null) return '';
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function escapeAttr(str) {
  return String(str).replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/"/g, '&quot;');
}

function timeAgo(ts) {
  if (!ts) return '—';
  const diff = Math.floor(Date.now() / 1000) - ts;
  if (diff < 0) return 'now';
  if (diff < 60) return 'just now';
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 7 * 86400) return `${Math.floor(diff / 86400)}d ago`;
  return new Date(ts * 1000).toLocaleDateString();
}

function clockTime(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function dayStart(ts) {
  const d = new Date(ts * 1000);
  d.setHours(0, 0, 0, 0);
  return d.getTime() / 1000;
}

function dateLabel(ts) {
  if (!ts) return '';
  const today = dayStart(Date.now() / 1000);
  const day = dayStart(ts);
  if (day === today) return 'Today';
  if (day === today - 86400) return 'Yesterday';
  const d = new Date(ts * 1000);
  return d.toLocaleDateString([], { day: 'numeric', month: 'short', year: d.getFullYear() !== new Date().getFullYear() ? 'numeric' : undefined });
}

function chatListTime(ts) {
  if (!ts) return '';
  const today = dayStart(Date.now() / 1000);
  const day = dayStart(ts);
  if (day === today) return clockTime(ts);
  if (day === today - 86400) return 'Yesterday';
  return new Date(ts * 1000).toLocaleDateString([], { day: 'numeric', month: 'short' });
}

function kindLabel(type) {
  if (type === 'group') return 'Group';
  if (type === 'channel') return 'Channel';
  if (type === 'dm') return 'DM';
  return type || 'Chat';
}

// ── Login page ────────────────────────────────────────
async function renderLogin(el) {
  let st = {};
  try { st = asObject(await callTool('status')); } catch { /* offline render */ }
  lastStatus = st;

  if (st.connected) {
    const name = String(st.bot_name || 'bot').replace(/^@/, '');
    el.innerHTML = `
      <div class="login-wrap">
        <div class="login-card">
          <div class="conn-hero">
            <div class="login-mark">✈</div>
            <div class="conn-hero-name">@${escapeHtml(name)}</div>
            <div class="conn-hero-id">bot id ${escapeHtml(String(st.bot_id || '—'))}</div>
            <div class="mt-16">
              <span class="badge badge-success">● Connected</span>
              ${st.privacy_mode ? '<span class="badge badge-info">Privacy ON</span>' : '<span class="badge badge-neutral">Privacy OFF</span>'}
            </div>
            <div class="conn-hero-actions">
              <button class="btn btn-primary" onclick="navigate('#/status')">Open Status</button>
              <button class="btn btn-ghost" onclick="handleDisconnect()">Disconnect</button>
            </div>
          </div>
        </div>
      </div>`;
    return;
  }

  el.innerHTML = `
    <div class="login-wrap">
      <div class="login-card">
        <div class="login-head">
          <div class="login-mark">✈</div>
          <div class="login-title">Connect Telegram</div>
          <div class="login-desc">Paste your Bot API token from @BotFather to link this channel.</div>
        </div>
        ${st.paired
          ? '<div class="alert alert-warning"><span>⚠</span><span>Token tersimpan tapi koneksi belum aktif. Coba login ulang atau cek jaringan.</span></div>'
          : ''}
        <div class="form-group">
          <label class="form-label" for="tokenInput">Bot Token</label>
          <div style="position:relative">
            <input type="password" class="form-input" id="tokenInput" autocomplete="off"
              placeholder="123456789:ABCdefGhIjKlMnOpQrStUvWxYz" />
          </div>
          <div class="form-hint">Token disimpan lokal saja (mode 0600) — tidak pernah dikirim ke mana pun.</div>
        </div>
        <button class="btn btn-primary btn-block" id="connectBtn" onclick="handleConnect()">Connect</button>
        <div class="login-footnote">
          Belum punya bot? <a href="https://t.me/BotFather" target="_blank" rel="noopener">@BotFather</a> → <code>/newbot</code>
        </div>
      </div>
    </div>`;
}

async function handleConnect() {
  const input = document.getElementById('tokenInput');
  const token = input.value.trim();
  if (!token) { showToast('Isi bot token dulu, tuan.', 'warning'); return; }
  const btn = document.getElementById('connectBtn');
  btn.disabled = true;
  btn.innerHTML = '<span class="spinner sm"></span> Connecting…';
  try {
    const r = await callTool('login', { bot_token: token });
    showToast(r && r.bot_name ? `Login sukses sebagai @${r.bot_name}` : 'Login sukses', 'success');
    navigate('#/status');
  } catch (e) {
    btn.disabled = false;
    btn.textContent = 'Connect';
    showToast(`Login gagal: ${e.message}`, 'error', 6000);
  }
}

// ── Status page ───────────────────────────────────────
async function renderStatus(el) {
  const st = asObject(await callTool('status'));
  lastStatus = st;
  const name = String(st.bot_name || '').replace(/^@/, '');
  const avatar = name ? name.charAt(0).toUpperCase() : '✈';
  const online = !!st.connected;

  const connections = [
    { label: 'Connection', value: online ? 'Long polling aktif' : st.paired ? 'Terkait, polling mati' : 'Belum terhubung', dot: online ? 'ok' : st.paired ? 'warn' : 'bad' },
    { label: 'Bot', value: st.bot_name ? `@${name}` : '—' },
    { label: 'Bot ID', value: st.bot_id != null ? String(st.bot_id) : '—' },
    { label: 'Privacy mode', value: st.privacy_mode ? 'ON (allowlist ditegakkan)' : 'OFF' },
    { label: 'Last event', value: st.last_event_at ? timeAgo(st.last_event_at) : '—' },
  ];

  el.innerHTML = `
    <div class="page">
      ${pageHeader('Status Bot', 'Keadaan koneksi, ingest, dan database', '')}

      <div class="card" style="display:flex;gap:18px;align-items:center;flex-wrap:wrap">
        <div class="chat-avatar k-dm" style="width:56px;height:56px;font-size:22px">${escapeHtml(avatar)}</div>
        <div style="flex:1;min-width:200px">
          <div style="font-size:17px;font-weight:750">${st.bot_name ? `@${escapeHtml(name)}` : 'Belum terhubung'}</div>
          <div class="mt-8">
            <span class="badge ${online ? 'badge-success' : st.paired ? 'badge-warning' : 'badge-danger'}">
              ${online ? '● Online' : st.paired ? '● Polling down' : '● Offline'}
            </span>
            ${st.privacy_mode ? '<span class="badge badge-info">Privacy ON</span>' : '<span class="badge badge-neutral">Privacy OFF</span>'}
          </div>
        </div>
        <button class="btn btn-secondary btn-sm" onclick="location.hash='#/settings'">Settings</button>
      </div>

      <div class="stats-grid">
        <div class="stat-card"><div class="stat-value">${st.message_count ?? '—'}</div><div class="stat-label">Pesan tersimpan</div></div>
        <div class="stat-card"><div class="stat-value">${st.chat_count ?? '—'}</div><div class="stat-label">Chat dilacak</div></div>
        <div class="stat-card"><div class="stat-value">${asArray(st.allowlist).length}</div><div class="stat-label">Allowlist</div></div>
        <div class="stat-card"><div class="stat-value">${st.paired ? '✓' : '✕'}</div><div class="stat-label">Token tersimpan</div></div>
      </div>

      <div class="card">
        <div class="card-title"><span class="card-emoji">🔌</span> Detail</div>
        <div class="card-row">
          ${connections.map((c) => `
            <div class="field">
              <div class="field-label">${c.label}</div>
              <div class="field-value">
                ${c.dot ? `<span class="status-dot ${c.dot}"></span>` : ''}${escapeHtml(String(c.value))}
              </div>
            </div>`).join('')}
        </div>
      </div>

      <div class="alert alert-info"><span>ℹ</span><span>
        <strong>Riwayat:</strong> hanya pesan sejak bot masuk ke chat yang tersimpan — Bot API memang tidak bisa mengambil pesan lama.
      </span></div>
    </div>`;
}

// ── Chats page ────────────────────────────────────────
async function renderChats(el) {
  const args = currentFilter !== 'all' ? { kind: currentFilter } : {};
  const data = asObject(await callTool('list_chats', args));
  const chats = asArray(data.chats);

  el.innerHTML = `
    <div class="page">
      ${pageHeader('Chats', `${chats.length} percakapan`, `
        <button class="btn btn-secondary btn-sm" onclick="handleRefresh()">↻ Refresh</button>`)}
      <div class="chats-toolbar">
        <div class="search-box">
          <span class="search-icon">⌕</span>
          <input type="text" id="chatSearch" placeholder="Cari pesan (FTS)" autocomplete="off" />
        </div>
        <div class="filter-tabs">
          ${['all', 'dm', 'group', 'channel'].map((k) => `
            <button class="filter-tab ${currentFilter === k ? 'active' : ''}" onclick="handleFilter('${k}')">
              ${k === 'all' ? 'All' : kindLabel(k)}
            </button>`).join('')}
        </div>
      </div>
      <div class="chat-list" id="chatList">
        ${chats.length === 0 ? emptyChatsState() : chats.map(renderChatItem).join('')}
      </div>
    </div>`;

  const search = document.getElementById('chatSearch');
  search.addEventListener('input', handleChatSearch);
}

function emptyChatsState() {
  return emptyState('💬', 'Belum ada chat', 'Chat muncul begitu bot menerima pesan.', true);
}

function renderChatItem(c) {
  const name = c.name || c.id || '?';
  const unread = Number(c.unread_count) || 0;
  return `
    <div class="chat-item" onclick="openChat('${escapeAttr(String(c.id))}')" role="button" tabindex="0">
      ${avatarHtml(name, c.type)}
      <div class="chat-info">
        <div class="chat-name">${escapeHtml(name)} <span class="kind-tag">${escapeHtml(kindLabel(c.type))}</span></div>
        <div class="chat-preview">${escapeHtml(c.last_message || '')}</div>
      </div>
      <div class="chat-meta">
        <div class="chat-time">${escapeHtml(chatListTime(c.last_message_at))}</div>
        ${unread > 0 ? `<span class="chat-unread">${unread > 99 ? '99+' : unread}</span>` : ''}
      </div>
    </div>`;
}

function openChat(chatId) {
  currentChatId = String(chatId);
  location.hash = `#/chat/${encodeURIComponent(currentChatId)}`;
}

async function handleRefresh() {
  await renderChats(document.getElementById('content'));
}

// Debounced FTS search rendered into the same list container.
function handleChatSearch() {
  const input = document.getElementById('chatSearch');
  if (!input) return;
  const query = input.value.trim();
  const list = document.getElementById('chatList');
  if (!list) return;

  if (!query) {
    renderChats(document.getElementById('content'));
    return;
  }

  clearTimeout(searchTimer);
  searchTimer = setTimeout(async () => {
    try {
      const data = asObject(await callTool('search_messages', { query, limit: 50 }));
      const results = asArray(data.messages);
      list.innerHTML = results.length === 0
        ? emptyState('🔍', 'Tidak ada hasil', `Tidak ada pesan tersimpan yang cocok dengan "${query}".`, true)
        : results.map(renderSearchResult).join('');
    } catch (e) {
      showToast('Pencarian gagal: ' + e.message, 'error');
    }
  }, 250);
}

function renderSearchResult(m) {
  return `
    <div class="chat-item" onclick="openChat('${escapeAttr(String(m.chat_id || ''))}')" role="button" tabindex="0">
      <div class="chat-avatar k-dm">🔍</div>
      <div class="chat-info">
        <div class="chat-name">${escapeHtml(m.sender_name || 'Unknown')} <span class="kind-tag">${escapeHtml(String(m.chat_id || '?'))}</span></div>
        <div class="chat-preview">${escapeHtml(m.text || '')}</div>
      </div>
      <div class="chat-meta"><div class="chat-time">${escapeHtml(clockTime(m.timestamp))}</div></div>
    </div>`;
}

async function handleFilter(filter) {
  currentFilter = filter;
  lastRenderSig = '';
  await renderChats(document.getElementById('content'));
}

async function refreshChatsSilently() {
  const args = currentFilter !== 'all' ? { kind: currentFilter } : {};
  const data = asObject(await callTool('list_chats', args));
  const sig = JSON.stringify(asArray(data.chats));
  if (sig === lastRenderSig) return;
  await renderChats(document.getElementById('content'));
}

// ── Chat thread page ──────────────────────────────────
let msgContainer = null;

async function renderChat(el) {
  const chatId = currentChatId;
  if (!chatId) { location.hash = '#/chats'; return; }

  const [chatRes, msgData] = await Promise.all([
    callTool('get_chat', { chat_id: chatId }).then((r) => asObject(r).chat).catch(() => null),
    callTool('get_messages', { chat_id: chatId, limit: 200 }),
  ]);

  const chat = asObject(chatRes);
  const chatName = chat.name || chatId;
  // Backend returns newest-first; a thread renders oldest→newest.
  const messages = asArray(msgData.messages).slice().reverse();

  el.innerHTML = `
    <div class="chat-shell">
      <div class="chat-header">
        <button class="icon-btn" title="Back" onclick="location.hash='#/chats'">←</button>
        ${avatarHtml(chatName, chat.type)}
        <div style="min-width:0">
          <div class="chat-header-name">${escapeHtml(String(chatName))}</div>
          <div class="chat-header-sub">${escapeHtml(kindLabel(chat.type))} · ${escapeHtml(String(chatId))}</div>
        </div>
        <div class="chat-header-actions">
          <button class="icon-btn" title="Refresh thread" onclick="refreshThreadNow()">↻</button>
        </div>
      </div>
      <div class="thread thread-wall" id="msgContainer"><div id="threadInner"></div></div>
      <div class="composer">
        <textarea class="composer-textarea" id="msgInput" rows="1"
          placeholder="Tulis pesan… (Enter kirim, Shift+Enter baris baru)"></textarea>
        <button class="send-btn" id="sendBtn" title="Send" onclick="sendMessage()">➤</button>
      </div>
    </div>`;

  msgContainer = document.getElementById('msgContainer');
  document.getElementById('threadInner').innerHTML = renderThread(messages);
  autoGrowComposer();
  document.getElementById('msgInput').addEventListener('input', autoGrowComposer);
  document.getElementById('msgInput').addEventListener('keydown', composerKeydown);
  scrollThreadToBottom(true);
  msgContainer.addEventListener('scroll', onThreadScroll);

  const sig = JSON.stringify(messages.map((m) => [m.id, m.text, m.timestamp, m.from_me]));
  lastRenderSig = 'thread:' + sig;
}

function renderThread(messages) {
  if (!messages.length) {
    return emptyState('📭', 'Belum ada pesan', 'Pesan akan muncul begitu bot menerimanya di chat ini.');
  }

  let html = '';
  let prevDay = null;
  let prevSender = null;
  let prevTs = 0;

  messages.forEach((m, idx) => {
    const day = dayStart(m.timestamp);
    if (day !== prevDay) {
      html += `<div class="date-sep">${dateLabel(m.timestamp)}</div>`;
      prevDay = day;
      prevSender = null;
    }

    // Group consecutive same-sender messages within ~4 minutes of each other.
    const isIn = !m.from_me;
    const sameSender = isIn && prevSender === m.sender_name && (m.timestamp - prevTs) < 4 * 60;
    const groupedClass = sameSender && idx > 0 ? ' grouped' : '';
    if (!sameSender && isIn) prevSender = m.sender_name;
    prevTs = m.timestamp;

    const senderTag = isIn && !sameSender && m.sender_name ? `<span class="bubble-sender">${escapeHtml(m.sender_name)}</span>` : '';
    const edited = m.edited_at ? '<span class="edited">(edited)</span>' : '';
    const meta = `<span class="bubble-meta">${clockTime(m.timestamp)} ${edited}</span>`;
    const body = escapeHtml(m.text || '');

    html += `
      <div class="msg-group">
        <div class="bubble-row ${isIn ? 'in' : 'out'}${groupedClass}">
          <div class="bubble ${isIn ? 'in' : 'out'}">${senderTag}${body}${meta}</div>
        </div>
      </div>`;
  });
  return html;
}

function onThreadScroll() {
  // Remember the "user is near bottom" flag so polling can preserve it.
  if (!msgContainer) return;
  const near = msgContainer.scrollHeight - msgContainer.scrollTop - msgContainer.clientHeight < 48;
  window.__nearBottom = near;
}

function scrollThreadToBottom(force) {
  if (!msgContainer) return;
  if (force || window.__nearBottom !== false) {
    msgContainer.scrollTop = msgContainer.scrollHeight;
  }
}

async function refreshThreadNow() {
  lastRenderSig = '';
  await renderChat(document.getElementById('content'));
}

async function refreshThreadSilently() {
  const chatId = currentChatId;
  if (!chatId) return;
  const msgData = asObject(await callTool('get_messages', { chat_id: chatId, limit: 200 }));
  const messages = asArray(msgData.messages).slice().reverse();
  const sig = JSON.stringify(messages.map((m) => [m.id, m.text, m.timestamp, m.from_me]));
  if (sig === lastRenderSig.replace(/^thread:/, '')) return;

  const inner = document.getElementById('threadInner');
  if (!inner) { await renderChat(document.getElementById('content')); return; }
  const wasAtBottom = !msgContainer || msgContainer.scrollHeight - msgContainer.scrollTop - msgContainer.clientHeight < 48;
  inner.innerHTML = renderThread(messages);
  if (wasAtBottom) scrollThreadToBottom(true);
  lastRenderSig = 'thread:' + sig;
  pollConnection(); // refresh sidebar/chip + approvals while we're at it
}

function autoGrowComposer() {
  const ta = document.getElementById('msgInput');
  if (!ta) return;
  ta.style.height = 'auto';
  ta.style.height = Math.min(ta.scrollHeight, 130) + 'px';
}

function composerKeydown(e) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    sendMessage();
  }
}

async function sendMessage() {
  const input = document.getElementById('msgInput');
  const btn = document.getElementById('sendBtn');
  if (!input) return;
  const text = input.value.trim();
  if (!text || !currentChatId) return;

  btn.disabled = true;
  btn.innerHTML = '<span class="spinner sm"></span>';
  try {
    await callTool('send_message', { chat_id: currentChatId, text });
    input.value = '';
    autoGrowComposer();
    // Re-render thread so the outbound message (mirrored by the backend)
    // shows up instantly, then scroll to bottom.
    lastRenderSig = '';
    await renderChat(document.getElementById('content'));
    scrollThreadToBottom(true);
  } catch (e) {
    showToast('Kirim gagal: ' + e.message, 'error');
  } finally {
    const tb = document.getElementById('sendBtn');
    if (tb) { tb.disabled = false; tb.innerHTML = '➤'; }
  }
}

// ── Approvals page ────────────────────────────────────
async function renderApprovals(el) {
  const data = asObject(await callTool('list_pending_approvals'));
  const approvals = asArray(data.approvals);

  el.innerHTML = `
    <div class="page">
      ${pageHeader('Pending Approvals', `${approvals.length} menunggu keputusan`, '')}
      ${approvals.length === 0
        ? emptyState('✓', 'Semua bersih', 'Tidak ada permintaan approval yang menunggu.')
        : approvals.map(renderApprovalCard).join('')}
    </div>`;
  updateApprovalBadge(true);
}

function renderApprovalCard(a) {
  const sender = a.sender_id || '?';
  return `
    <div class="approval-card">
      ${avatarHtml(sender, '')}
      <div class="approval-body">
        <div class="approval-head">
          <span class="approval-sender">${escapeHtml(sender)}</span>
          ${a.chat_id ? `<span class="approval-chat">di ${escapeHtml(String(a.chat_id))}</span>` : ''}
          <span class="approval-time">${escapeHtml(timeAgo(a.time))}</span>
        </div>
        <div class="approval-payload">${escapeHtml(a.text || '')}</div>
        <div class="approval-actions">
          <button class="btn btn-success btn-sm" onclick="handleApproval('${escapeAttr(String(a.id))}','approve')">✓ Allow once</button>
          <button class="btn btn-primary btn-sm" onclick="handleApproval('${escapeAttr(String(a.id))}','always')">★ Always allow</button>
          <button class="btn btn-danger btn-sm" onclick="handleApproval('${escapeAttr(String(a.id))}','deny')">✕ Deny</button>
        </div>
      </div>
    </div>`;
}

async function handleApproval(id, action) {
  try {
    const list = asObject(await callTool('list_pending_approvals'));
    const app = asArray(list.approvals).find((a) => String(a.id) === String(id));
    // Perilaku "Always allow": add pendorong ke allowlist agar diizinkan
    // permanen, lalu resolve callback sebagai approve.
    if (action === 'always') {
      if (app && app.sender_id) {
        await callTool('add_to_allowlist', { user_id: app.sender_id });
      }
      await callTool('answer_callback', { callback_query_id: id, text: 'Approved ✓ (allowlisted)' });
      showToast('Disetujui & ditambahkan ke allowlist', 'success');
    } else if (action === 'approve') {
      await callTool('answer_callback', { callback_query_id: id, text: 'Approved ✓' });
      showToast('Disetujui', 'success');
    } else {
      await callTool('answer_callback', { callback_query_id: id, text: 'Denied ❌' });
      showToast('Ditolak', 'info');
    }
    await renderApprovals(document.getElementById('content'));
  } catch (e) {
    showToast('Aksi approval gagal: ' + e.message, 'error');
  }
}

// ── Settings page ─────────────────────────────────────
async function renderSettings(el) {
  const st = asObject(await callTool('status'));
  lastStatus = st;
  const allowlist = asArray(st.allowlist);
  const connected = !!st.connected;
  const paired = !!st.paired;
  const botName = String(st.bot_name || '—');

  el.innerHTML = `
    <div class="page">
      ${pageHeader('Settings', 'Kelola koneksi, allowlist, dan preferensi', '')}

      <div class="section-title">Koneksi</div>
      <div class="card">
        <div class="card-title"><span class="card-emoji">🔑</span> Bot</div>
        <div class="toggle-row">
          <div class="toggle-copy">
            <div class="toggle-title">${connected ? `@${escapeHtml(botName.replace(/^@/, ''))}` : (paired ? 'Terkait tapi offline' : 'Belum terhubung')}</div>
            <div class="toggle-desc">${connected ? 'Long polling aktif · ' : ''}bot id ${escapeHtml(String(st.bot_id || '—'))}</div>
          </div>
          <span class="badge ${connected ? 'badge-success' : 'badge-danger'}">${connected ? 'Online' : 'Offline'}</span>
        </div>
        <div class="input-with-action mt-8">
          <input type="password" class="form-input" id="settingsToken" placeholder="Token baru (kosongkan untuk tidak mengganti)" autocomplete="off" />
          <button class="btn btn-secondary" onclick="changeToken()">Ganti Token</button>
          <button class="btn btn-danger" onclick="handleDisconnect()">Disconnect</button>
        </div>
        <div class="form-hint">Mengganti token akan logout lalu login ulang dengan token baru.</div>
      </div>

      <div class="section-title">Privasi</div>
      <div class="card">
        <div class="toggle-row">
          <div class="toggle-copy">
            <div class="toggle-title">Enforce allowlist (privacy mode)</div>
            <div class="toggle-desc">Saat ON, hanya pengguna di allowlist yang boleh berinteraksi. Yang lain di-drop senyap.</div>
          </div>
          <label class="toggle">
            <input type="checkbox" id="privacyToggle" ${st.privacy_mode ? 'checked' : ''}
              onchange="handlePrivacyToggle(this.checked)" />
            <span class="track"></span>
          </label>
        </div>
        <div class="alert alert-warning mt-8"><span>⚠</span><span>
          Ini gerbang lokal plugin. Mode privasi Telegram (BotFather → /setprivacy) tetap diatur terpisah agar bot bisa membaca semua pesan grup.
        </span></div>
      </div>

      <div class="section-title">Allowlist</div>
      <div class="card">
        <div class="card-title"><span class="card-emoji">📋</span> Pengguna / chat yang diizinkan (${allowlist.length})</div>
        <div id="allowlistContainer">
          ${allowlist.length === 0
            ? `<div class="empty-state" style="padding:20px"><div class="empty-state-icon">📋</div><div class="empty-state-title" style="font-size:14px">Allowlist kosong</div><div class="empty-state-desc" style="font-size:12.5px">Tambahkan user ID numerik untuk mengizinkannya.</div></div>`
            : allowlist.map((id) => `
              <div class="allowlist-item">
                <span class="allowlist-id">${escapeHtml(String(id))}</span>
                <button class="btn btn-danger btn-sm" onclick="removeAllowlist('${escapeAttr(String(id))}')">Hapus</button>
              </div>`).join('')}
        </div>
        <div class="input-with-action mt-8">
          <input type="text" class="form-input" id="addAllowlistInput" placeholder="User ID numerik (int64)" autocomplete="off" />
          <button class="btn btn-primary" onclick="addAllowlist()">Tambah</button>
        </div>
      </div>

      <div class="section-title">Tentang</div>
      <div class="card about-footer">
        NusaShell Telegram v0.2.0 · Bot API 10.3 · library <span class="mono">mymrac/telego</span> · data lokal SQLite (WAL + FTS5).
      </div>
    </div>`;
}

async function handlePrivacyToggle(enabled) {
  try {
    await callTool('set_privacy_mode', { enabled });
    showToast(enabled ? 'Privacy mode ON' : 'Privacy mode OFF', 'success');
  } catch (e) {
    const t = document.getElementById('privacyToggle');
    if (t) t.checked = !enabled;
    showToast('Gagal ubah privacy mode: ' + e.message, 'error');
  }
}

async function changeToken() {
  const input = document.getElementById('settingsToken');
  const token = input.value.trim();
  if (!token) { showToast('Isi token baru dulu, tuan.', 'warning'); return; }
  const ok = await showConfirm('Ganti bot token', 'Bot akan logout dulu lalu login dengan token baru. Lanjutkan?', 'Ganti Token');
  if (!ok) return;
  try {
    await callTool('logout');
    await callTool('login', { bot_token: token });
    input.value = '';
    showToast('Token berhasil diganti', 'success');
    location.hash = '#/status';
  } catch (e) {
    showToast('Gagal ganti token: ' + e.message, 'error', 6000);
  }
}

async function handleDisconnect() {
  const ok = await showConfirm('Disconnect bot', 'Token akan dihapus dan polling dihentikan. Anda perlu login ulang nanti. Lanjutkan?', 'Disconnect');
  if (!ok) return;
  try {
    await callTool('logout');
    showToast('Bot diputuskan', 'info');
    currentChatId = null;
    lastRenderSig = '';
    location.hash = '#/login';
  } catch (e) {
    showToast('Gagal disconnect: ' + e.message, 'error');
  }
}

async function addAllowlist() {
  const input = document.getElementById('addAllowlistInput');
  const id = input.value.trim();
  if (!id) { showToast('Masukkan user ID dulu.', 'warning'); return; }
  try {
    await callTool('add_to_allowlist', { user_id: id });
    input.value = '';
    showToast(`Ditambahkan: ${id}`, 'success');
    await renderSettings(document.getElementById('content'));
  } catch (e) {
    showToast('Gagal tambah: ' + e.message, 'error');
  }
}

async function removeAllowlist(id) {
  try {
    await callTool('remove_from_allowlist', { user_id: id });
    showToast('Dihapus dari allowlist', 'info');
    await renderSettings(document.getElementById('content'));
  } catch (e) {
    showToast('Gagal hapus: ' + e.message, 'error');
  }
}

// ── Init ──────────────────────────────────────────────
window.addEventListener('hashchange', () => render(location.hash));
window.addEventListener('popstate', () => render(location.hash));
window.addEventListener('DOMContentLoaded', () => {
  if (!location.hash) history.replaceState(null, '', '#/login');
  render(location.hash);
  startPolling();
});

// Expoes global handlers used from inline onclick (kept on window for clarity).
Object.assign(window, {
  navigate,
  handleConnect,
  handleDisconnect,
  handleRefresh,
  handleFilter,
  openChat,
  handleChatSearch,
  handleApproval,
  handlePrivacyToggle,
  changeToken,
  addAllowlist,
  removeAllowlist,
  sendMessage,
  refreshThreadNow,
  handleConfirmOk,
  handleConfirmCancel,
});