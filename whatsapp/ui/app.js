// WhatsApp plugin UI — QR login + chat list.
// Communicates with the MCP server via the NusaShell plugin bridge.

(function () {
  "use strict";

  // --- State ---
  let paired = false;
  let connected = false;
  let activeChatJID = null;
  let chats = [];
  let pollTimer = null;
  let mainPollTimer = null;
  let mainPollBusy = false;

  // --- DOM ---
  const loginView = document.getElementById("loginView");
  const mainView = document.getElementById("mainView");
  const qrContainer = document.getElementById("qrContainer");
  const getQrBtn = document.getElementById("getQrBtn");
  const refreshQrBtn = document.getElementById("refreshQrBtn");
  const loginStatus = document.getElementById("loginStatus");
  const searchInput = document.getElementById("searchInput");
  const chatList = document.getElementById("chatList");
  const chatName = document.getElementById("chatName");
  const chatKind = document.getElementById("chatKind");
  const messageList = document.getElementById("messageList");
  const messageInput = document.getElementById("messageInput");
  const sendBtn = document.getElementById("sendBtn");
  const status = document.getElementById("status");

  // Pair-code mode DOM (see ui/index.html).
  const tabQr = document.getElementById("tabQr");
  const tabCode = document.getElementById("tabCode");
  const qrPanel = document.getElementById("qrPanel");
  const codePanel = document.getElementById("codePanel");
  const phoneInput = document.getElementById("phoneInput");
  const getCodeBtn = document.getElementById("getCodeBtn");
  const refreshCodeBtn = document.getElementById("refreshCodeBtn");
  const codeContainer = document.getElementById("codeContainer");
  const codeDisplay = document.getElementById("codeDisplay");
  const codeCountdown = document.getElementById("codeCountdown");
  const codeWarn = document.getElementById("codeWarn");

  // Active mode: "qr" (default) or "code".
  let activeMode = "qr";

  // Countdown ticker for the pair-code expiry. We tick once a second so
  // the user sees the remaining lifetime of the issued code.
  let codeCountdownTimer = null;

  // --- NusaShell MCP bridge ---
  // The plugin UI calls MCP tools via the NusaShell bridge API that the
  // plugin handler injects as window.shell (see transport/plugin_handler.go
  // injectShim). The shim signature is callTool(pluginId, toolName, args).
  // The plugin ID is passed by the host as a ?pluginId= query param; fall
  // back to this plugin's manifest id when opened standalone.
  var pluginId = new URLSearchParams(location.search).get("pluginId") ||
    "nusashell.whatsapp";

  // callTool unwraps the bridge envelope and surfaces tool-level errors
  // (IsError=true) as rejections so callers' catch paths render the real
  // message. Mirrors kanban/ui-src/api/client.ts.
  async function callTool(name, args) {
    if (!window.shell || !window.shell.callTool) {
      throw new Error("NusaShell bridge not available");
    }
    const envelope = await window.shell.callTool(pluginId, name, args || {});
    // Older bridges returned { requestId, result }; unwrap that too.
    let payload = envelope;
    if (payload && typeof payload === "object" && "result" in payload) {
      payload = payload.result;
    }
    // Tool-level errors: throw the content text so the UI shows it.
    if (payload && typeof payload === "object" && "isError" in payload) {
      if (payload.isError) {
        const text = (payload.content || [])
          .map(function (c) { return c && c.text; })
          .filter(Boolean)
          .join("\n");
        throw new Error(text || "Tool " + name + " failed");
      }
    }
    return payload;
  }

  function setStatus(msg, isError) {
    if (status) {
      status.textContent = msg;
      status.className = isError ? "error" : "";
    }
  }

  function setLoginStatus(msg, isError, isSuccess) {
    if (loginStatus) {
      loginStatus.textContent = msg;
      loginStatus.className = isError ? "error" : isSuccess ? "success" : "";
    }
  }

  // --- QR Login ---
  async function getQrCode() {
    setLoginStatus("Requesting QR code...");
    getQrBtn.disabled = true;
    try {
      const result = await callTool("login");
      const data = parseResult(result);
      if (data && data.qr_code) {
        renderQR(data.qr_code);
        setLoginStatus("Scan the QR with your phone. Expires in ~60s.");
        refreshQrBtn.style.display = "inline-block";
        getQrBtn.style.display = "none";
        // Start polling for pairing success.
        startPairingPoll();
      } else if (data && data.hint) {
        // Tool succeeded but returned a hint (e.g. already paired).
        setLoginStatus(data.hint, true);
        getQrBtn.disabled = false;
      } else {
        setLoginStatus("Unexpected response from login tool — got: " + JSON.stringify(data), true);
        getQrBtn.disabled = false;
      }
    } catch (err) {
      // Surface the actual whatsmeow error message — it usually contains a
      // specific reason ("session logged out", "websocket closed", etc.)
      // that the LLM/operator needs to act on. Fall back to a generic
      // message only if the bridge gives us nothing.
      const msg = (err && err.message) ? err.message : String(err);
      setLoginStatus("Failed to get QR: " + msg, true);
      getQrBtn.disabled = false;
    }
  }

  function renderQR(qrString) {
    // Render the QR code string as an SVG image using a lightweight
    // QR rendering approach. We use an external QR library if available,
    // otherwise fall back to displaying the raw string.
    if (qrContainer) {
      qrContainer.innerHTML = "";
      // Use a QR code image API as fallback for rendering.
      // The QR string is the raw content to encode.
      const img = document.createElement("img");
      img.alt = "WhatsApp QR Code";
      img.src = "https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=" +
        encodeURIComponent(qrString);
      img.onerror = function () {
        qrContainer.innerHTML = '<div class="qr-placeholder">QR string: <br><code>' +
          escapeHtml(qrString.substring(0, 80)) + "...</code></div>";
      };
      qrContainer.appendChild(img);
    }
  }

  function startPairingPoll() {
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = setInterval(checkPairingStatus, 3000);
  }

  function stopPairingPoll() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function startMainPolling() {
    stopMainPolling();
    mainPollTimer = setInterval(refreshMainView, 7000);
  }

  function stopMainPolling() {
    if (mainPollTimer) {
      clearInterval(mainPollTimer);
      mainPollTimer = null;
    }
  }

  async function refreshMainView() {
    if (!paired || mainPollBusy) return;
    mainPollBusy = true;
    try {
      await loadChats(true);
      if (activeChatJID) await loadMessages(activeChatJID, true);
    } finally {
      mainPollBusy = false;
    }
  }

  async function checkPairingStatus() {
    try {
      const result = await callTool("status");
      const data = parseResult(result);
      if (data && data.paired) {
        stopPairingPoll();
        paired = true;
        connected = data.connected;
        showMainView();
        await loadChats();
        startMainPolling();
      } else if (data && !data.connected && data.awaiting_qr === false) {
        // The server tore down the login socket without pairing (server-side
        // rejection, cooldown, or a network drop). Surface this in the login
        // status so the user knows to retry with a fresh QR — silently
        // re-polling hides the failure.
        setLoginStatus(
          "Pairing socket closed before scan. Click 'Refresh QR' for a fresh code.",
          true
        );
      }
    } catch (err) {
      // Ignore polling errors — keep trying.
    }
  }

  // --- Pair-code mode ---
  function setMode(mode) {
    activeMode = mode;
    if (mode === "qr") {
      tabQr.classList.add("active");
      tabQr.setAttribute("aria-selected", "true");
      tabCode.classList.remove("active");
      tabCode.setAttribute("aria-selected", "false");
      qrPanel.style.display = "";
      qrPanel.setAttribute("aria-hidden", "false");
      codePanel.style.display = "none";
      codePanel.setAttribute("aria-hidden", "true");
      stopCodeCountdown();
    } else {
      tabCode.classList.add("active");
      tabCode.setAttribute("aria-selected", "true");
      tabQr.classList.remove("active");
      tabQr.setAttribute("aria-selected", "false");
      qrPanel.style.display = "none";
      qrPanel.setAttribute("aria-hidden", "true");
      codePanel.style.display = "";
      codePanel.setAttribute("aria-hidden", "false");
      // Focus the phone input for fast entry.
      if (phoneInput) phoneInput.focus();
    }
    if (tabQr) tabQr.tabIndex = mode === "qr" ? 0 : -1;
    if (tabCode) tabCode.tabIndex = mode === "code" ? 0 : -1;
    // Clear any lingering status from the other mode.
    setLoginStatus("");
  }

  function handleTabKeydown(event) {
    if (!event || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    let next = activeMode;
    if (event.key === "ArrowLeft") next = activeMode === "qr" ? "code" : "qr";
    if (event.key === "ArrowRight") next = activeMode === "qr" ? "code" : "qr";
    if (event.key === "Home") next = "qr";
    if (event.key === "End") next = "code";
    setMode(next);
    const target = next === "qr" ? tabQr : tabCode;
    if (target) target.focus();
  }

  function startCodeCountdown(expiresAtUnix) {
    stopCodeCountdown();
    const update = () => {
      const now = Math.floor(Date.now() / 1000);
      const remaining = Math.max(0, expiresAtUnix - now);
      if (codeCountdown) codeCountdown.textContent = String(remaining);
      if (remaining <= 0) {
        stopCodeCountdown();
        setLoginStatus("Pairing code expired. Click 'New Code' to issue a fresh one.", true);
        if (codeDisplay) codeDisplay.textContent = "----";
      }
    };
    update();
    codeCountdownTimer = setInterval(update, 1000);
  }

  function stopCodeCountdown() {
    if (codeCountdownTimer) {
      clearInterval(codeCountdownTimer);
      codeCountdownTimer = null;
    }
  }

  async function getPairCode() {
    const phone = phoneInput ? phoneInput.value.trim() : "";
    if (!phone) {
      setLoginStatus("Enter a phone number first.", true);
      if (phoneInput) phoneInput.focus();
      return;
    }

    setLoginStatus("Requesting pairing code...");
    getCodeBtn.disabled = true;
    if (codeWarn) codeWarn.style.display = "none";

    try {
      const result = await callTool("pair_with_code", { phone: phone });
      const data = parseResult(result);
      if (data && data.pair_code) {
        if (codeDisplay) codeDisplay.textContent = data.pair_code;
        if (codeContainer) codeContainer.style.display = "";
        if (codeWarn) {
          // Detect server-side rejection that mentions the consumer-account
          // limitation (e.g. "not authorized", "business only") and tell
          // the user up front. We don't try to be exhaustive — we just
          // surface a hint when the response looks like an authorization
          // failure. A clean normalise-then-issue path returns without
          // touching this element.
          const allText = JSON.stringify(data).toLowerCase();
          if (allText.indexOf("not authorized") !== -1 ||
              allText.indexOf("business") !== -1 ||
              allText.indexOf("forbidden") !== -1 ||
              allText.indexOf("405") !== -1) {
            codeWarn.style.display = "";
          }
        }
        const expiresAt = data.expires_at || 0;
        if (expiresAt > 0) {
          startCodeCountdown(expiresAt);
        }
        setLoginStatus(
          "Pairing code issued. Enter it on your phone within " +
            (data.expires_at ? Math.max(0, Math.floor(data.expires_at - Date.now() / 1000)) : 120) +
            "s.",
          false,
          true
        );
        getCodeBtn.style.display = "none";
        refreshCodeBtn.style.display = "inline-block";
        // Start polling so we transition to the main view once the phone
        // accepts the code.
        startPairingPoll();
      } else if (data && data.hint) {
        setLoginStatus(data.hint, true);
        getCodeBtn.disabled = false;
      } else {
        setLoginStatus("Unexpected response from pair_with_code — got: " + JSON.stringify(data), true);
        getCodeBtn.disabled = false;
      }
    } catch (err) {
      // Surface the actual whatsmeow error. Common cases:
      //   - "phone number is empty after stripping non-digits" → user typo
      //   - "phone number has 5 digits — too short" → user typo
      //   - "issue pair code: <server error>" → WA server rejected
      const msg = (err && err.message) ? err.message : String(err);
      setLoginStatus("Failed to get pairing code: " + msg, true);
      if (codeWarn) {
        const lc = msg.toLowerCase();
        if (lc.indexOf("not authorized") !== -1 ||
            lc.indexOf("business") !== -1 ||
            lc.indexOf("forbidden") !== -1 ||
            lc.indexOf("405") !== -1 ||
            lc.indexOf("rate") !== -1) {
          codeWarn.style.display = "";
        }
      }
      getCodeBtn.disabled = false;
    }
  }

  // --- Main view ---
  function showMainView() {
    loginView.style.display = "none";
    mainView.style.display = "flex";
  }

  function showLoginView() {
    stopMainPolling();
    mainView.style.display = "none";
    loginView.style.display = "flex";
  }

  async function loadChats(silent) {
    if (!silent) setStatus("Loading chats...");
    try {
      const result = await callTool("list_chats", { limit: 50 });
      const data = parseResult(result);
      chats = (data && data.chats) || [];
      renderChatList(filterChats(chats));
      if (!silent) setStatus("Loaded " + chats.length + " chats");
    } catch (err) {
      if (!silent) setStatus("Failed to load chats: " + (err.message || err), true);
    }
  }

  function filterChats(chatsToFilter) {
    const q = searchInput ? searchInput.value.toLowerCase().trim() : "";
    if (!q) return chatsToFilter;
    return chatsToFilter.filter(function (c) {
      return (
        (c.name || "").toLowerCase().includes(q) ||
        (c.last_message || "").toLowerCase().includes(q) ||
        (c.chat_jid || "").toLowerCase().includes(q)
      );
    });
  }

  function renderChatList(chatsToRender) {
    if (!chatList) return;
    chatList.innerHTML = "";
    if (chatsToRender.length === 0) {
      chatList.innerHTML = '<div class="empty-state">No chats yet</div>';
      return;
    }
    chatsToRender.forEach(function (chat) {
      const item = document.createElement("div");
      item.className = "chat-item";
      item.setAttribute("role", "button");
      item.tabIndex = 0;
      if (chat.chat_jid === activeChatJID) item.classList.add("active");
      item.dataset.jid = chat.chat_jid;

      const name = document.createElement("div");
      name.className = "chat-item-name";
      name.textContent = chat.name || chat.chat_jid.split("@")[0];

      const meta = document.createElement("div");
      meta.className = "chat-item-meta";

      const preview = document.createElement("div");
      preview.className = "chat-item-preview";
      preview.textContent = chat.last_message || "";

      const right = document.createElement("div");
      right.style.display = "flex";
      right.style.alignItems = "center";
      right.style.gap = "6px";

      if (chat.kind && chat.kind !== "dm") {
        const kindTag = document.createElement("span");
        kindTag.className = "kind-tag";
        kindTag.textContent = chat.kind;
        right.appendChild(kindTag);
      }

      if (chat.unread_count > 0) {
        const badge = document.createElement("span");
        badge.className = "unread-badge";
        badge.textContent = String(chat.unread_count);
        right.appendChild(badge);
      }

      meta.appendChild(preview);
      meta.appendChild(right);
      item.appendChild(name);
      item.appendChild(meta);

      function activateChat() {
        selectChat(chat.chat_jid, chat.name || chat.chat_jid.split("@")[0], chat.kind);
      }
      item.addEventListener("click", activateChat);
      item.addEventListener("keydown", function (event) {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          activateChat();
        }
      });
      chatList.appendChild(item);
    });
  }

  async function selectChat(jid, name, kind) {
    activeChatJID = jid;
    chatName.textContent = name;
    chatKind.textContent = kind || "";
    // Update active state in list.
    document.querySelectorAll(".chat-item").forEach(function (el) {
      el.classList.toggle("active", el.dataset.jid === jid);
    });
    await loadMessages(jid);
    // Mark read.
    try {
      await callTool("mark_read", { chat_jid: jid });
    } catch (err) {
      // Non-fatal.
    }
  }

  async function loadMessages(jid, silent) {
    if (!silent) setStatus("Loading messages...");
    try {
      const result = await callTool("get_messages", { chat_jid: jid, limit: 50 });
      const data = parseResult(result);
      const messages = (data && data.messages) || [];
      // A user may switch chats while the request is in flight. Do not let a
      // slower response overwrite the newly selected thread.
      if (jid !== activeChatJID) return;
      const nearBottom = !messageList ||
        messageList.scrollHeight - messageList.scrollTop - messageList.clientHeight < 80;
      renderMessages(messages);
      if (nearBottom && messageList) messageList.scrollTop = messageList.scrollHeight;
      if (!silent) setStatus("");
    } catch (err) {
      if (!silent) setStatus("Failed to load messages: " + (err.message || err), true);
    }
  }

  function renderMessages(msgs) {
    if (!messageList) return;
    messageList.innerHTML = "";
    if (msgs.length === 0) {
      messageList.innerHTML = '<div class="empty-state">No messages in this chat</div>';
      return;
    }
    // Messages come newest-first; we display in reverse (oldest at top).
    msgs.slice().reverse().forEach(function (msg) {
      const div = document.createElement("div");
      div.className = "message " + (msg.from_me ? "from-me" : "from-them");
      if (msg.deleted_at) {
        div.classList.add("deleted");
        div.textContent = "🚫 This message was deleted";
      } else if (msg.has_media && !msg.text) {
        div.classList.add("media");
        div.textContent = "📎 " + (msg.kind || "media");
      } else {
        if (!msg.from_me && msg.sender_name && msg.sender_name !== "You") {
          const sender = document.createElement("div");
          sender.className = "sender";
          sender.textContent = msg.sender_name;
          div.appendChild(sender);
        }
        const text = document.createElement("div");
        text.textContent = msg.text || "";
        div.appendChild(text);
      }
      const ts = document.createElement("div");
      ts.className = "timestamp";
      ts.textContent = formatTime(msg.timestamp);
      div.appendChild(ts);
      messageList.appendChild(div);
    });
  }

  async function sendMessage() {
    const jid = activeChatJID;
    const text = messageInput ? messageInput.value.trim() : "";
    if (!jid) {
      setStatus("Select a chat before sending.", true);
      return;
    }
    if (!text) {
      if (messageInput) messageInput.focus();
      return;
    }

    if (sendBtn) sendBtn.disabled = true;
    if (messageInput) messageInput.disabled = true;
    setStatus("Sending...");
    try {
      await callTool("send_message", { chat_jid: jid, text: text });
      if (messageInput) {
        messageInput.value = "";
        messageInput.style.height = "";
      }
      await loadChats(true);
      await loadMessages(jid, true);
      setStatus("");
    } catch (err) {
      setStatus("Failed to send message: " + (err.message || err), true);
    } finally {
      if (sendBtn) sendBtn.disabled = false;
      if (messageInput) {
        messageInput.disabled = false;
        messageInput.focus();
      }
    }
  }

  function autoGrowMessageInput() {
    if (!messageInput) return;
    messageInput.style.height = "auto";
    messageInput.style.height = Math.min(messageInput.scrollHeight, 120) + "px";
  }

  // --- Search ---
  if (searchInput) {
    searchInput.addEventListener("input", function () {
      renderChatList(filterChats(chats));
    });
  }

  // --- Helpers ---
  // parseResult extracts the tool payload from the bridge result.
  // Prioritizes structuredContent (native object) over the JSON text in
  // content[0].text, matching kanban/notes. Falls back to content text,
  // then the raw envelope.
  function parseResult(result) {
    if (result == null) return result;
    // structuredContent is the native object the server set (helpers.go
    // jsonResult sets both Content and StructuredContent to the same data).
    if (result.structuredContent != null) return result.structuredContent;
    // Fall back to parsing the text content part.
    if (result.content && result.content[0] && result.content[0].text) {
      try {
        return JSON.parse(result.content[0].text);
      } catch (e) {
        return null;
      }
    }
    return result;
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function formatTime(unix) {
    if (!unix) return "";
    const d = new Date(unix * 1000);
    const now = new Date();
    if (d.toDateString() === now.toDateString()) {
      return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    }
    return d.toLocaleDateString([], { month: "short", day: "numeric" }) +
      " " + d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  // --- Init ---
  getQrBtn.addEventListener("click", getQrCode);
  refreshQrBtn.addEventListener("click", getQrCode);
  if (tabQr) {
    tabQr.addEventListener("click", function () { setMode("qr"); });
    tabQr.addEventListener("keydown", handleTabKeydown);
  }
  if (tabCode) {
    tabCode.addEventListener("click", function () { setMode("code"); });
    tabCode.addEventListener("keydown", handleTabKeydown);
  }
  if (getCodeBtn) getCodeBtn.addEventListener("click", getPairCode);
  if (refreshCodeBtn) refreshCodeBtn.addEventListener("click", getPairCode);
  if (phoneInput) {
    phoneInput.addEventListener("keydown", function (e) {
      if (e && e.key === "Enter") {
        e.preventDefault();
        getPairCode();
      }
    });
  }

  if (messageInput) {
    messageInput.addEventListener("input", autoGrowMessageInput);
    messageInput.addEventListener("keydown", function (event) {
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        sendMessage();
      }
    });
  }
  if (sendBtn) sendBtn.addEventListener("click", sendMessage);

  // Stop the pairing and main-view polls + countdown when the window closes
  // so we don't leak timers or intervals.
  window.addEventListener("beforeunload", function () {
    stopPairingPoll();
    stopMainPolling();
    stopCodeCountdown();
  });

  // Check initial status.
  async function init() {
    try {
      const result = await callTool("status");
      const data = parseResult(result);
      if (data && data.paired) {
        paired = true;
        connected = data.connected;
        showMainView();
        await loadChats();
        startMainPolling();
      } else {
        showLoginView();
      }
    } catch (err) {
      // If the bridge isn't available, show the login view.
      showLoginView();
      setLoginStatus("Click 'Get QR Code' to link your WhatsApp account.");
    }
  }

  init();
})();
