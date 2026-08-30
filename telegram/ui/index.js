/* ═══════════════════════════════════════════════════════
   NusaShell Telegram — Frontend
   Plain HTML/JS, no Node build. Talks to the MCP backend
   exclusively via the NusaShell plugin bridge (window.shell).
   No mock data, no local fallback — ever.
   ═══════════════════════════════════════════════════════ */

// ── NusaShell MCP bridge ──
// The host (transport/plugin_handler.go injectShim) injects
// window.shell.callTool(pluginId, toolName, args). The plugin
// id is passed by the host as a ?pluginId= query param; fall
// back to this plugin's manifest id when opened standalone.
const PLUGIN_ID =
  new URLSearchParams(location.search).get('pluginId') || 'nusashell.telegram';

// ── Toast notification system ──
function showToast(message, type = 'info', duration = 3500) {
  const container = document.getElementById('toast-container');
  if (!container) return;
  const toast = document.createElement('div');
  toast.className = `toast ${type}`;
  const icons = { success: '✅', error: '❌', info: 'ℹ️', warning: '⚠️' };
  toast.innerHTML = `
    <span class="toast-icon">${icons[type] || 'ℹ️'}</span>
    <span class="toast-msg">${escapeHtml(message)}</span>
    <span class="toast-close" onclick="this.parentElement.remove()">✕</span>
  `;
  container.appendChild(toast);
  requestAnimationFrame(() => toast.classList.add('show'));
  setTimeout(() => {
    toast.classList.remove('show');
    toast.classList.add('fade-out');
    setTimeout(() => toast.remove(), 300);
  }, duration);
}

// ── Confirm dialog system ──
let confirmResolve = null;

function showConfirm(title, message) {
  return new Promise((resolve) => {
    const overlay = document.getElementById('confirm-dialog');
    const titleEl = document.getElementById('confirm-dialog-title');
    const msgEl = document.getElementById('confirm-dialog-message');
    if (!overlay || !titleEl || !msgEl) { resolve(false); return; }
    titleEl.textContent = title;
    msgEl.textContent = message;
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

// callTool unwraps the bridge envelope and surfaces tool-level
// errors (isError=true) as rejections so callers' catch paths
// render the real message. Mirrors kanban/ui-src/api/client.ts.
async function callTool(tool, args = {}) {
  if (!window.shell || typeof window.shell.callTool !== 'function') {
    throw new Error('NusaShell shell not available. Please open this plugin through NusaShell.');
  }
  const envelope = await window.shell.callTool(PLUGIN_ID, tool, args);

  // Older bridges returned { requestId, result }; unwrap that.
  let payload = envelope;
  if (payload && typeof payload === 'object' && 'result' in payload) {
    payload = payload.result;
  }

  // Tool-level errors: throw the content text so the UI shows it.
  if (payload && typeof payload === 'object' && 'isError' in payload) {
    if (payload.isError) {
      const text = (payload.content || [])
        .map((c) => c && c.text)
        .filter(Boolean)
        .join('\n');
      throw new Error(text || `Tool ${tool} failed`);
    }
  }

  // Extract structuredContent — the actual tool data. Without this
  // the whole envelope is returned and array access on an object
  // crashes consumers with "X is not iterable".
  if (payload && typeof payload === 'object' && 'structuredContent' in payload) {
    const sc = payload.structuredContent;
    if (sc !== null && sc !== undefined) {
      payload = sc;
    } else {
      // structuredContent was null (nil slice/value). Fall back to
      // parsing the text content so callers still get the real value
      // (e.g. [] for an empty list) instead of the whole envelope.
      const text = (payload.content || [])
        .map((c) => c && c.text)
        .filter(Boolean)
        .join('\n');
      if (typeof text === 'string') {
        try { payload = JSON.parse(text); } catch { /* keep envelope */ }
      }
    }
  }

  // Some MCP servers wrap array results in { items: [...] }.
  if (payload && typeof payload === 'object' && 'items' in payload && Array.isArray(payload.items)) {
    return payload.items;
  }

  return payload;
}

// Coerce a maybe-array field from the backend into a real array.
// Defensive only — never substitutes mock data.
function asArray(value) {
  return Array.isArray(value) ? value : [];
}

// ── State ──
let currentChatId = null;
let currentFilter = 'all';
let searchTimer = null;

// ── Navigation ──
function navigate(hash) {
  const page = (hash.replace(/^#\/?/, '').split('/')[0]) || 'login';
  const links = document.querySelectorAll('.nav-item');
  links.forEach((l) => {
    l.classList.toggle('active', l.dataset.page === page);
  });
  renderPage(page);
}

// ── Page Renderer ──
async function renderPage(page) {
  const content = document.getElementById('content');
  content.innerHTML = '<div class="loading-overlay"><span class="spinner"></span></div>';

  try {
    switch (page) {
      case 'login': await renderLogin(content); break;
      case 'status': await renderStatus(content); break;
      case 'chats': await renderChats(content); break;
      case 'chat': await renderChat(content); break;
      case 'approvals': await renderApprovals(content); break;
      case 'settings': await renderSettings(content); break;
      default: renderHome(content);
    }
  } catch (e) {
    showToast(e.message, 'error');
  }
}

// ── Login ──
async function renderLogin(el) {
  el.innerHTML = `
    <div class="page">
      <div class="login-container">
        <div class="login-card">
          <div class="login-icon">🔌</div>
          <div class="login-title">Connect Telegram</div>
          <div class="login-desc">Paste your Bot API token from @BotFather to link this channel.</div>
          <div class="form-group">
            <label class="form-label">Bot Token</label>
            <input type="password" class="form-input" id="tokenInput"
              placeholder="123456789:ABCdefGhIjKlMnOpQrStUvWxYz" />
            <div class="form-hint">Tokens are stored locally only. Never logged.</div>
          </div>
          <button class="btn btn-primary btn-block" id="connectBtn" onclick="handleConnect()">Connect</button>
          <div style="text-align:center;margin-top:20px;font-size:13px;color:var(--text-muted)">
            Need a bot? <a href="https://t.me/BotFather" target="_blank">@BotFather</a> → <code>/newbot</code>
          </div>
        </div>
      </div>
    </div>
  `;
}

async function handleConnect() {
  const input = document.getElementById('tokenInput');
  const token = input.value.trim();
  if (!token) { showToast('Please paste a bot token.', 'warning'); return; }

  const btn = document.getElementById('connectBtn');
  btn.disabled = true;
  btn.innerHTML = '<span class="spinner"></span> Connecting...';

  try {
    await callTool('login', { bot_token: token });
    navigate('#/status');
  } catch (e) {
    btn.disabled = false;
    btn.innerHTML = 'Connect';
    showToast('Login failed: ' + e.message, 'error');
  }
}

// ── Status ──
async function renderStatus(el) {
  const data = await callTool('status');
  el.innerHTML = `
    <div class="page">
      <div class="page-header">
        <div>
          <div class="page-title">Bot Status</div>
          <div class="page-subtitle">Connection state, ingestion freshness, and database stats</div>
        </div>
        <a href="#/settings" class="btn btn-secondary btn-sm">Settings</a>
      </div>

      <div class="card">
        <div class="card-title">Connection</div>
        <div class="card-row">
          <div class="field">
            <div class="field-label">Bot</div>
            <div class="field-value">
              <span class="status-dot ${data.connected ? 'online' : 'offline'}"></span>
              ${data.connected ? `@${escapeHtml(data.botName)}` : 'Not connected'}
            </div>
          </div>
          <div class="field">
            <div class="field-label">Bot ID</div>
            <div class="field-value">${data.botId != null ? escapeHtml(String(data.botId)) : '—'}</div>
          </div>
          <div class="field">
            <div class="field-label">Privacy Mode</div>
            <div class="field-value">${data.privacyMode ? 'ON (groups)' : 'OFF'}</div>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-title">Database</div>
        <div class="card-row">
          <div class="field">
            <div class="field-label">Messages Stored</div>
            <div class="field-value">${data.messagesStored}</div>
          </div>
          <div class="field">
            <div class="field-label">Chats Tracked</div>
            <div class="field-value">${data.chatsTracked}</div>
          </div>
          <div class="field">
            <div class="field-label">Last Activity</div>
            <div class="field-value">${data.lastActivity ? timeAgo(data.lastActivity) : '—'}</div>
          </div>
        </div>
      </div>

      <div class="alert alert-info">
        <strong>ℹ️ History:</strong> Only messages since the bot was added to this chat are stored.
        No magic workaround exists — Bot API limitation.
      </div>
    </div>
  `;
}

// ── Chats ──
async function renderChats(el) {
  const args = currentFilter !== 'all' ? { kind: currentFilter } : {};
  const data = await callTool('list_chats', args);
  const chats = asArray(data.chats);

  el.innerHTML = `
    <div class="page">
      <div class="page-header">
        <div>
          <div class="page-title">Chats</div>
          <div class="page-subtitle">${chats.length} conversations</div>
        </div>
        <button class="btn btn-secondary btn-sm" onclick="handleRefresh()">↻ Refresh</button>
      </div>
      <div class="toolbar">
        <input type="text" class="search-input" id="chatSearch" placeholder="Search messages..."
          oninput="handleChatSearch()" />
        <div class="filter-tabs">
          <button class="filter-tab ${currentFilter === 'all' ? 'active' : ''}" onclick="handleFilter('all')">All</button>
          <button class="filter-tab ${currentFilter === 'dm' ? 'active' : ''}" onclick="handleFilter('dm')">DM</button>
          <button class="filter-tab ${currentFilter === 'group' ? 'active' : ''}" onclick="handleFilter('group')">Group</button>
          <button class="filter-tab ${currentFilter === 'channel' ? 'active' : ''}" onclick="handleFilter('channel')">Channel</button>
        </div>
      </div>
      <div class="card">
        <div class="chat-list" id="chatList">
          ${chats.length === 0 ? emptyChatsState() : chats.map((c) => renderChatItem(c)).join('')}
        </div>
      </div>
    </div>
  `;
}

function emptyChatsState() {
  return `
    <div class="empty-state">
      <div class="empty-state-icon">💬</div>
      <div class="empty-state-title">No chats yet</div>
      <div class="empty-state-desc">Chats appear here once the bot receives messages.</div>
    </div>
  `;
}

function renderChatItem(c) {
  const initial = (c.name || '?').charAt(0);
  return `
    <div class="chat-item" onclick="openChat('${escapeAttr(String(c.id))}')">
      <div class="chat-avatar">${escapeHtml(initial)}</div>
      <div class="chat-info">
        <div class="chat-name">${escapeHtml(c.name || c.id)}</div>
        <div class="chat-preview">${escapeHtml(c.preview || '')}</div>
      </div>
      <div class="chat-meta">
        <div class="chat-time">${escapeHtml(c.time || '')}</div>
        ${c.unread ? `<span class="chat-unread">${escapeHtml(String(c.unread))}</span>` : ''}
      </div>
    </div>
  `;
}

async function openChat(chatId) {
  currentChatId = chatId;
  navigate(`#/chat/${chatId}`);
}

async function renderChat(el) {
  const chatId = currentChatId;
  if (!chatId) { navigate('#/chats'); return; }

  // Fetch chat metadata and messages from the backend in parallel.
  let chatData = null;
  const [chatRes, msgData] = await Promise.all([
    callTool('get_chat', { chat_id: chatId }).then((r) => r).catch(() => null),
    callTool('get_messages', { chat_id: chatId }),
  ]);
  chatData = chatRes;
  const messages = asArray(msgData.messages);

  const chatName = (chatData && chatData.name) || chatId;
  const chatKind = chatData && chatData.kind ? String(chatData.kind).toUpperCase() : 'chat';

  el.innerHTML = `
    <div class="page" style="padding:0;display:flex;flex-direction:column;height:100%">
      <div class="page-header" style="padding:16px 20px;border-bottom:1px solid var(--border)">
        <div style="display:flex;align-items:center;gap:12px">
          <button class="btn btn-secondary btn-sm" onclick="navigate('#/chats')">← Back</button>
          <div>
            <div class="page-title">${escapeHtml(String(chatName))}</div>
            <div class="page-subtitle">${escapeHtml(chatKind)}</div>
          </div>
        </div>
      </div>
      <div class="card" style="flex:1;overflow-y:auto;margin:16px;border-radius:var(--radius)" id="msgContainer">
        ${messages.length === 0 ? emptyMessagesState() : messages.map((m) => renderMessageBubble(m)).join('')}
      </div>
      <div class="input-area">
        <input type="text" class="form-input" id="msgInput" placeholder="Type a message..."
          onkeydown="if(event.key==='Enter')sendMessage()" />
        <button class="btn btn-primary" onclick="sendMessage()">▶ Send</button>
      </div>
    </div>
  `;
  const container = document.getElementById('msgContainer');
  if (container) container.scrollTop = container.scrollHeight;
}

function emptyMessagesState() {
  return `
    <div class="empty-state">
      <div class="empty-state-icon">📭</div>
      <div class="empty-state-title">No messages yet</div>
      <div class="empty-state-desc">Messages appear once the bot receives them in this chat.</div>
    </div>
  `;
}

function renderMessageBubble(m) {
  const isOutbound = m.outbound;
  return `
    <div>
      <div class="message-bubble ${isOutbound ? 'outbound' : 'inbound'}">${escapeHtml(m.text)}</div>
      <div class="message-meta">${escapeHtml(m.from || '')} · ${escapeHtml(m.time || '')}</div>
    </div>
  `;
}

async function sendMessage() {
  const input = document.getElementById('msgInput');
  const text = input.value.trim();
  if (!text) return;
  try {
    await callTool('send_message', { chat_id: currentChatId, text });
    input.value = '';
    renderChat(document.getElementById('content'));
  } catch (e) {
    showToast('Send failed: ' + e.message, 'error');
  }
}

async function handleRefresh() { renderChats(document.getElementById('content')); }

// Search messages via the backend (full-text across stored messages),
// rendered into the chat list area. No DOM filtering, no mock data.
async function handleChatSearch() {
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
      const data = await callTool('search_messages', { query });
      const results = asArray(data.messages);
      list.innerHTML = results.length === 0
        ? emptySearchState(query)
        : results.map((m) => renderSearchResult(m)).join('');
    } catch (e) {
      showToast('Search failed: ' + e.message, 'error');
    }
  }, 250);
}

function renderSearchResult(m) {
  return `
    <div class="chat-item" onclick="openChat('${escapeAttr(String(m.chat_id || m.chatId || ''))}')">
      <div class="chat-avatar">🔍</div>
      <div class="chat-info">
        <div class="chat-name">${escapeHtml(m.from || 'Unknown')} · ${escapeHtml(String(m.chat_id || m.chatId || '?'))}</div>
        <div class="chat-preview">${escapeHtml(m.text || '')}</div>
      </div>
      <div class="chat-meta">
        <div class="chat-time">${escapeHtml(m.time || '')}</div>
      </div>
    </div>
  `;
}

function emptySearchState(query) {
  return `
    <div class="empty-state">
      <div class="empty-state-icon">🔍</div>
      <div class="empty-state-title">No results</div>
      <div class="empty-state-desc">No stored messages match "${escapeHtml(query)}".</div>
    </div>
  `;
}

async function handleFilter(filter) {
  currentFilter = filter;
  renderChats(document.getElementById('content'));
}

// ── Approvals ──
async function renderApprovals(el) {
  const data = await callTool('list_pending_approvals');
  const approvals = asArray(data.approvals);

  el.innerHTML = `
    <div class="page">
      <div class="page-header">
        <div>
          <div class="page-title">Pending Approvals</div>
          <div class="page-subtitle">Review and approve before the bot executes</div>
        </div>
      </div>
      ${approvals.length === 0 ? `
        <div class="empty-state">
          <div class="empty-state-icon">✅</div>
          <div class="empty-state-title">All clear</div>
          <div class="empty-state-desc">No pending approval requests.</div>
        </div>
      ` : approvals.map((a) => `
        <div class="approval-card">
          <div class="approval-text"><strong>${escapeHtml(a.chat || '')}</strong> — ${escapeHtml(a.time || '')}</div>
          <div class="approval-text" style="margin-bottom:12px">"${escapeHtml(a.text || '')}"</div>
          <div class="approval-actions">
            <button class="btn btn-success btn-sm" onclick="handleApproval('${escapeAttr(String(a.id))}','approve')">✅ Allow once</button>
            <button class="btn btn-primary btn-sm" onclick="handleApproval('${escapeAttr(String(a.id))}','always')">✅ Always allow</button>
            <button class="btn btn-danger btn-sm" onclick="handleApproval('${escapeAttr(String(a.id))}','deny')">❌ Deny</button>
          </div>
        </div>
      `).join('')}
    </div>
  `;
}

async function handleApproval(id, action) {
  try {
    await callTool('answer_callback', { callback_query_id: id, text: action === 'deny' ? 'Denied ❌' : 'Approved ✓' });
    navigate('#/approvals');
  } catch (e) {
    showToast('Approval failed: ' + e.message, 'error');
  }
}

// ── Settings ──
async function renderSettings(el) {
  // Allowlist is loaded from the backend via status (no mock list).
  let allowlist = [];
  try {
    const status = await callTool('status');
    allowlist = asArray(status.allowlist);
  } catch { /* show empty allowlist on error */ }

  el.innerHTML = `
    <div class="page">
      <div class="page-header">
        <div>
          <div class="page-title">Settings</div>
          <div class="page-subtitle">Manage bot connection, allowlist, and preferences</div>
        </div>
      </div>

      <div class="card">
        <div class="card-title">Bot Token</div>
        <div class="card-row">
          <div class="field" style="flex:3">
            <input type="password" class="form-input" id="settingsToken" placeholder="••••••••••••" />
          </div>
          <div style="display:flex;gap:8px;flex:1">
            <button class="btn btn-secondary btn-sm" onclick="changeToken()">Change</button>
            <button class="btn btn-danger btn-sm" onclick="handleDisconnect()">Disconnect</button>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-title">Privacy Mode (groups)</div>
        <div class="card-row">
          <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
            <input type="checkbox" id="privacyToggle" checked /> ON — Bot only sees messages mentioning it or sent as replies.
          </label>
        </div>
        <div class="form-hint" style="margin-top:8px">To see all messages: BotFather → /setprivacy → OFF, then remove & re-add bot.</div>
      </div>

      <div class="card">
        <div class="card-title">Allowlist</div>
        <div id="allowlistContainer">
          ${allowlist.length === 0 ? `
            <div class="empty-state">
              <div class="empty-state-icon">📋</div>
              <div class="empty-state-title">Allowlist empty</div>
              <div class="empty-state-desc">Add a numeric user ID or @username to permit it.</div>
            </div>
          ` : allowlist.map((id) => `
            <div style="display:flex;align-items:center;justify-content:space-between;padding:6px 0;border-bottom:1px solid var(--border-light)">
              <span style="font-family:var(--font-mono);font-size:13px">${escapeHtml(String(id))}</span>
              <button class="btn btn-danger btn-sm" onclick="removeAllowlist('${escapeAttr(String(id))}')">Remove</button>
            </div>
          `).join('')}
        </div>
        <div style="display:flex;gap:8px;margin-top:12px">
          <input type="text" class="form-input" id="addAllowlistInput" placeholder="Numeric user ID or @username"
            style="flex:1" />
          <button class="btn btn-primary btn-sm" onclick="addAllowlist()">Add</button>
        </div>
      </div>

      <div class="card">
        <div class="card-title">Notifications</div>
        <div style="display:flex;flex-direction:column;gap:8px">
          <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
            <input type="checkbox" checked /> Sound on new message
          </label>
          <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
            <input type="checkbox" /> Show preview
          </label>
        </div>
      </div>

      <div class="alert alert-info">
        <strong>ℹ️ About:</strong> NusaShell Telegram v0.1.0 · Bot API 10.3 · Library: mymrac/telego
      </div>
    </div>
  `;
}

async function changeToken() {
  const input = document.getElementById('settingsToken');
  if (!input.value.trim()) { showToast('Paste a new token.', 'warning'); return; }
  try {
    await callTool('logout');
    await callTool('login', { bot_token: input.value });
    navigate('#/status');
  } catch (e) {
    showToast('Token change failed: ' + e.message, 'error');
  }
}

async function handleDisconnect() {
  try {
    await callTool('logout');
    navigate('#/login');
  } catch (e) {
    showToast('Disconnect failed: ' + e.message, 'error');
  }
}

async function addAllowlist() {
  const input = document.getElementById('addAllowlistInput');
  const id = input.value.trim();
  if (!id) return;
  try {
    await callTool('add_to_allowlist', { user_id: id });
    renderSettings(document.getElementById('content'));
  } catch (e) {
    showToast('Add failed: ' + e.message, 'error');
  }
}

async function removeAllowlist(id) {
  try {
    await callTool('remove_from_allowlist', { user_id: id });
    renderSettings(document.getElementById('content'));
  } catch (e) {
    showToast('Remove failed: ' + e.message, 'error');
  }
}

// ── Home ──
function renderHome(el) {
  navigate('#/login');
}

// ── Utilities ──
function escapeHtml(str) {
  if (str == null) return '';
  const div = document.createElement('div');
  div.textContent = String(str);
  return div.innerHTML;
}

function escapeAttr(str) {
  return String(str).replace(/'/g, "\\'").replace(/"/g, '&quot;');
}

function timeAgo(ts) {
  if (!ts) return '—';
  const diff = Math.floor(Date.now() / 1000) - ts;
  if (diff < 60) return 'just now';
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

// ── Init ──
window.addEventListener('hashchange', () => navigate(location.hash));
window.addEventListener('DOMContentLoaded', () => {
  navigate(location.hash || '#/login');
  // Poll status every 30s when on status/chat page.
  setInterval(() => {
    const hash = location.hash;
    if (hash.startsWith('#/status') || hash.startsWith('#/chat')) {
      callTool('status').catch(() => {});
    }
  }, 30000);
});
