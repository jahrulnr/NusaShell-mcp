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
  const status = document.getElementById("status");

  // --- NusaShell MCP bridge ---
  // The plugin UI calls MCP tools via the NusaShell bridge API.
  // In standalone mode (no bridge), it falls back to a status display.
  function callTool(name, args) {
    if (window.nusashell && window.nusashell.callTool) {
      return window.nusashell.callTool(name, args || {});
    }
    return Promise.reject(new Error("NusaShell bridge not available"));
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
        setLoginStatus(data.hint, true);
        getQrBtn.disabled = false;
      } else {
        setLoginStatus("Unexpected response from login tool", true);
        getQrBtn.disabled = false;
      }
    } catch (err) {
      setLoginStatus("Failed to get QR: " + (err.message || err), true);
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
      }
    } catch (err) {
      // Ignore polling errors — keep trying.
    }
  }

  // --- Main view ---
  function showMainView() {
    loginView.style.display = "none";
    mainView.style.display = "flex";
  }

  function showLoginView() {
    mainView.style.display = "none";
    loginView.style.display = "flex";
  }

  async function loadChats() {
    setStatus("Loading chats...");
    try {
      const result = await callTool("list_chats", { limit: 50 });
      const data = parseResult(result);
      chats = (data && data.chats) || [];
      renderChatList(chats);
      setStatus("Loaded " + chats.length + " chats");
    } catch (err) {
      setStatus("Failed to load chats: " + (err.message || err), true);
    }
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

      if (chat.kind === "group") {
        const kindTag = document.createElement("span");
        kindTag.className = "kind-tag";
        kindTag.textContent = "group";
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

      item.addEventListener("click", function () {
        selectChat(chat.chat_jid, chat.name || chat.chat_jid.split("@")[0], chat.kind);
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

  async function loadMessages(jid) {
    setStatus("Loading messages...");
    try {
      const result = await callTool("get_messages", { chat_jid: jid, limit: 50 });
      const data = parseResult(result);
      const messages = (data && data.messages) || [];
      renderMessages(messages);
      setStatus("");
    } catch (err) {
      setStatus("Failed to load messages: " + (err.message || err), true);
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

  // --- Search ---
  if (searchInput) {
    searchInput.addEventListener("input", function () {
      const q = searchInput.value.toLowerCase().trim();
      if (!q) {
        renderChatList(chats);
        return;
      }
      const filtered = chats.filter(function (c) {
        return (
          (c.name || "").toLowerCase().includes(q) ||
          (c.last_message || "").toLowerCase().includes(q) ||
          (c.chat_jid || "").toLowerCase().includes(q)
        );
      });
      renderChatList(filtered);
    });
  }

  // --- Helpers ---
  function parseResult(result) {
    if (result && result.content && result.content[0] && result.content[0].text) {
      try {
        return JSON.parse(result.content[0].text);
      } catch (e) {
        return null;
      }
    }
    if (result && result.structuredContent) return result.structuredContent;
    return result;
  }

  function escapeHtml(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
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
