import {
  chooseInitialAccountId,
  EMAIL_FRAME_SANDBOX,
  mailFrameDocument,
  preferredMailBody,
  readableMailError,
} from "./mail-ui-state.js";

const pluginId = new URLSearchParams(location.search).get("pluginId") || "nusashell.mail";

const providerPresets = {
  gmail: {
    imap: { host: "imap.gmail.com", port: 993, security: "tls" },
    smtp: { host: "smtp.gmail.com", port: 465, security: "tls" },
  },
  yahoo: {
    imap: { host: "imap.mail.yahoo.com", port: 993, security: "tls" },
    smtp: { host: "smtp.mail.yahoo.com", port: 465, security: "tls" },
  },
  icloud: {
    imap: { host: "imap.mail.me.com", port: 993, security: "tls" },
    smtp: { host: "smtp.mail.me.com", port: 587, security: "starttls" },
  },
};

const state = {
  accounts: [],
  selectedAccountId: null,
  selectedMailboxId: "INBOX",
  selectedMessageKey: null,
  messages: [],
  mailboxes: [],
  toastTimer: null,
};

const elements = Object.fromEntries([
  "account-count",
  "account-list",
  "folder-list",
  "unified-count",
  "message-list",
  "message-total",
  "message-list-context",
  "message-list-title",
  "reading-empty",
  "message-detail",
  "detail-account",
  "detail-subject",
  "detail-avatar",
  "detail-sender",
  "detail-recipients",
  "detail-date",
  "detail-body",
  "attachment-list",
  "refresh-button",
  "add-account-button",
  "close-message-button",
  "search-form",
  "mail-search-input",
  "sync-caption",
  "connection-label",
  "toast",
  "account-modal",
  "account-modal-title",
  "account-modal-close",
  "account-form",
  "account-id",
  "account-name",
  "account-email",
  "account-username",
  "account-password",
  "account-password-help",
  "provider-note",
  "account-enabled",
  "imap-host",
  "imap-port",
  "imap-security",
  "smtp-host",
  "smtp-port",
  "smtp-security",
  "account-form-error",
  "save-account-button",
  "delete-account-button",
  "cancel-account-button",
  "confirm-modal",
  "confirm-modal-title",
  "confirm-modal-copy",
  "confirm-modal-close",
  "confirm-modal-cancel",
  "confirm-modal-submit",
].map((id) => [id, document.getElementById(id)]));

function parseToolResult(result) {
  if (result?.structuredContent) return result.structuredContent;
  if (result?.result != null && !Array.isArray(result?.result)) return result.result;
  const containers = [
    result?.content,
    result?.result?.content,
    result?.result,
    result,
  ];
  for (const container of containers) {
    if (!Array.isArray(container)) continue;
    const text = container.find((item) => typeof item?.text === "string")?.text;
    if (!text) continue;
    try {
      return JSON.parse(text);
    } catch {
      throw new Error(text);
    }
  }
  return result ?? {};
}

async function callTool(name, args = {}) {
  const result = await window.shell.callTool(pluginId, name, args);
  return parseToolResult(result);
}

async function initialize() {
  bindEvents();
  await refreshSettings();
  if (state.accounts.length === 0) {
    renderAccounts();
    renderFolders();
    renderNoAccounts();
    openAccountModal();
    return;
  }
  state.selectedAccountId = chooseInitialAccountId(
    state.accounts,
    state.selectedAccountId,
  );
  renderAccounts();
  await loadMailboxes(state.selectedAccountId);
  updateListHeading();
  await loadInbox();
}

async function refreshSettings() {
  const result = await callTool("accounts", {});
  state.accounts = result.accounts ?? [];
  if (state.selectedAccountId &&
      !state.accounts.some((account) => account.id === state.selectedAccountId)) {
    state.selectedAccountId = null;
  }
  renderAccounts();
}

async function loadInbox() {
  showMessageSkeleton();
  setConnectionState("Loading inbox…", true);
  try {
    const data = state.selectedAccountId
      ? await callTool("messages", {
          account_id: state.selectedAccountId,
          mailbox_id: state.selectedMailboxId,
          limit: 50,
        })
      : await callTool("inbox", { limit: 50 });
    state.messages = data.messages ?? [];
    renderMessages();
    elements["message-total"].textContent = `${data.total ?? state.messages.length} messages`;
    elements["unified-count"].textContent = state.selectedAccountId ? "" : String(data.total ?? state.messages.length);
    setConnectionState("Mail MCP ready", false);
  } catch (error) {
    renderListState("Inbox unavailable", readableMailError(error));
    setConnectionState("Connection needs attention", false);
  }
}

async function loadMailboxes(accountId) {
  state.mailboxes = [];
  renderFolders();
  if (!accountId) return;
  try {
    const result = await callTool("mailboxes", { account_id: accountId });
    state.mailboxes = result.mailboxes ?? [];
    renderFolders();
  } catch (error) {
    showToast(readableMailError(error), true);
  }
}

async function selectAccount(accountId) {
  state.selectedAccountId = accountId;
  state.selectedMailboxId = "INBOX";
  state.selectedMessageKey = null;
  closeMessage();
  renderAccounts();
  await loadMailboxes(accountId);
  updateListHeading();
  await loadInbox();
}

async function selectFolder(mailboxId) {
  if (!state.selectedAccountId && mailboxId !== "INBOX") return;
  state.selectedMailboxId = mailboxId;
  state.selectedMessageKey = null;
  closeMessage();
  renderFolders();
  updateListHeading();
  await loadInbox();
}

async function openMessage(message) {
  const key = `${message.accountId}:${message.mailboxId}:${message.uid}`;
  state.selectedMessageKey = key;
  document.querySelector(".mail-shell")?.classList.add("message-open");
  renderMessages();
  elements["reading-empty"].hidden = true;
  elements["message-detail"].hidden = false;
  elements["detail-subject"].textContent = "Loading message…";
  elements["detail-body"].textContent = "";
  elements["attachment-list"].replaceChildren();
  try {
    const result = await callTool("read", {
      account_id: message.accountId,
      mailbox_id: message.mailboxId,
      uid: message.uid,
    });
    renderMessageDetail(result.message);
  } catch (error) {
    elements["detail-subject"].textContent = "Message unavailable";
    elements["detail-body"].textContent = readableMailError(error);
  }
}

async function searchMail(query) {
  showMessageSkeleton();
  elements["message-list-title"].textContent = "Search";
  elements["message-list-context"].textContent = `“${query.slice(0, 60)}”`;
  try {
    const result = await callTool("search", {
      query,
      ...(state.selectedAccountId ? { account_ids: [state.selectedAccountId] } : {}),
      mailbox_id: state.selectedMailboxId,
      limit: 50,
    });
    state.messages = result.messages ?? [];
    renderMessages();
    elements["message-total"].textContent = `${result.total ?? state.messages.length} matches`;
  } catch (error) {
    renderListState("Search failed", readableMailError(error));
  }
}

function renderAccounts() {
  const list = elements["account-list"];
  list.replaceChildren();
  elements["account-count"].textContent = String(state.accounts.length);

  for (const account of state.accounts) {
    const row = create("div", "account-row");
    const button = document.createElement("button");
    button.type = "button";
    button.className = `account-item${state.selectedAccountId === account.id ? " active" : ""}`;
    button.title = `Open ${account.name}`;
    button.append(
      avatar(account.name, "account-avatar"),
      accountCopy(account),
      stateDot(account.enabled),
    );
    button.addEventListener("click", () => void selectAccount(account.id));
    const editButton = textElement("button", "account-edit-button", "Edit");
    editButton.type = "button";
    editButton.title = `Edit or delete ${account.name}`;
    editButton.setAttribute("aria-label", `Edit or delete ${account.name}`);
    editButton.addEventListener("click", () => openAccountModal(account));
    row.append(button, editButton);
    list.append(row);
  }
}

function renderFolders() {
  const list = elements["folder-list"];
  list.replaceChildren();
  list.append(folderButton("INBOX", state.selectedAccountId ? "Inbox" : "Unified inbox", "⌂"));
  if (!state.selectedAccountId) return;
  for (const mailbox of state.mailboxes) {
    if (mailbox.id.toUpperCase() === "INBOX") continue;
    const icon = specialUseIcon(mailbox.specialUse);
    list.append(folderButton(mailbox.id, mailbox.name, icon, mailbox.unread));
  }
}

function renderMessages() {
  const list = elements["message-list"];
  list.replaceChildren();
  if (state.messages.length === 0) {
    renderListState("Nothing here", "No messages match this view.");
    return;
  }
  for (const message of state.messages) {
    const key = `${message.accountId}:${message.mailboxId}:${message.uid}`;
    const button = document.createElement("button");
    button.type = "button";
    button.className = [
      "message-card",
      message.unread ? "unread" : "",
      state.selectedMessageKey === key ? "active" : "",
    ].filter(Boolean).join(" ");
    button.setAttribute("role", "listitem");
    button.append(create("span", "unread-marker"));

    const content = create("div", "message-card-content");
    const meta = create("div", "message-meta");
    meta.append(
      textElement("span", "message-from", senderLabel(message.from)),
      textElement("time", "message-date", shortDate(message.date)),
    );
    const subjectRow = create("div", "message-subject-row");
    subjectRow.append(textElement("span", "message-subject", message.subject || "(no subject)"));
    if (message.starred) subjectRow.append(textElement("span", "star-mark", "★"));
    if (message.hasAttachments) subjectRow.append(textElement("span", "attachment-mark", "⌕"));
    content.append(meta, subjectRow);
    content.append(textElement(
      "p",
      "message-excerpt",
      message.excerpt || `${accountName(message.accountId)} · ${message.unread ? "Unread" : "Read"}`,
    ));
    button.append(content);
    button.addEventListener("click", () => void openMessage(message));
    list.append(button);
  }
}

function renderMessageDetail(message) {
  elements["detail-account"].textContent = accountName(message.accountId);
  elements["detail-subject"].textContent = message.subject || "(no subject)";
  elements["detail-avatar"].textContent = initials(senderLabel(message.from));
  elements["detail-sender"].textContent = senderLabel(message.from);
  elements["detail-recipients"].textContent = `to ${formatAddresses(message.to) || "you"}`;
  elements["detail-date"].textContent = longDate(message.date);
  renderMessageBody(message);

  const attachments = elements["attachment-list"];
  attachments.replaceChildren();
  for (const attachment of message.attachments ?? []) {
    attachments.append(textElement(
      "span",
      "attachment-chip",
      `⌕ ${attachment.filename} · ${formatBytes(attachment.size)}`,
    ));
  }
}

function renderNoAccounts() {
  updateListHeading();
  renderListState(
    "Connect your first account",
    "Add an IMAP and SMTP account. App passwords are recommended for providers that support them.",
  );
}

function renderListState(title, copy) {
  const list = elements["message-list"];
  list.replaceChildren();
  const stateElement = create("div", "list-state");
  stateElement.append(
    textElement("strong", "", title),
    textElement("span", "", copy),
  );
  list.append(stateElement);
}

function showMessageSkeleton() {
  const list = elements["message-list"];
  list.replaceChildren();
  const skeleton = create("div", "list-skeleton");
  for (let index = 0; index < 6; index += 1) skeleton.append(create("div", "skeleton-row"));
  list.append(skeleton);
}

function openAccountModal(account = null) {
  elements["account-form"].reset();
  elements["account-form-error"].textContent = "";
  elements["account-password"].placeholder = account
    ? "Leave blank to keep the saved password"
    : "Required for a new account";
  elements["account-modal-title"].textContent = account ? "Edit mail account" : "Add mail account";
  elements["delete-account-button"].hidden = !account;
  elements["account-id"].value = account?.id ?? "";
  elements["account-name"].value = account?.name ?? "";
  elements["account-email"].value = account?.email ?? "";
  elements["account-username"].value = account?.username ?? "";
  elements["account-enabled"].checked = account?.enabled ?? true;
  elements["imap-host"].value = account?.incoming?.host ?? "";
  elements["imap-port"].value = String(account?.incoming?.port ?? 993);
  elements["imap-security"].value = account?.incoming?.secure === false ? "starttls" : "tls";
  elements["smtp-host"].value = account?.outgoing?.host ?? "";
  elements["smtp-port"].value = String(account?.outgoing?.port ?? 465);
  elements["smtp-security"].value = account?.outgoing?.secure === false ? "starttls" : "tls";
  selectProviderPill(providerForAccount(account));
  elements["account-modal"].hidden = false;
  queueMicrotask(() => elements["account-name"].focus());
}

function closeAccountModal() {
  elements["account-modal"].hidden = true;
}

async function saveAccount(event) {
  event.preventDefault();
  setAccountFormBusy(true);
  elements["account-form-error"].textContent = "";
  try {
    const email = elements["account-email"].value.trim();
    const id = elements["account-id"].value || accountIdFromEmail(email);
    const input = {
      id,
      name: elements["account-name"].value,
      email,
      username: elements["account-username"].value || email,
      password: elements["account-password"].value,
      enabled: elements["account-enabled"].checked,
      imap: serverForm("imap"),
      smtp: serverForm("smtp"),
    };
    await callTool("account_save", input);
    await refreshSettings();
    closeAccountModal();
    state.selectedAccountId = input.id;
    state.selectedMailboxId = "INBOX";
    renderAccounts();
    await loadMailboxes(input.id);
    updateListHeading();
    await loadInbox();
    showToast(`${input.name} saved.`);
  } catch (error) {
    elements["account-form-error"].textContent = readableMailError(error);
  } finally {
    setAccountFormBusy(false);
  }
}

async function deleteCurrentAccount() {
  const id = elements["account-id"].value;
  const account = state.accounts.find((item) => item.id === id);
  if (!account || !(await confirmAccountDelete(account.name))) {
    return;
  }
  try {
    const result = await callTool("account_delete", { account_id: id });
    state.accounts = result.accounts ?? [];
    state.selectedAccountId = null;
    closeAccountModal();
    renderAccounts();
    renderFolders();
    if (state.accounts.length > 0) await loadInbox();
    else renderNoAccounts();
    showToast(`${account.name} removed.`);
  } catch (error) {
    elements["account-form-error"].textContent = readableMailError(error);
  } finally {
    setAccountFormBusy(false);
  }
}

function confirmAccountDelete(name) {
  elements["confirm-modal-title"].textContent = "Delete account?";
  elements["confirm-modal-copy"].textContent = `Delete ${name} from NusaShell Mail? Messages on the server are not deleted.`;
  elements["confirm-modal"].hidden = false;
  return new Promise((resolve) => {
    const close = (result) => {
      elements["confirm-modal"].hidden = true;
      elements["confirm-modal-cancel"].onclick = null;
      elements["confirm-modal-submit"].onclick = null;
      elements["confirm-modal-close"].onclick = null;
      resolve(result);
    };
    elements["confirm-modal-cancel"].onclick = () => close(false);
    elements["confirm-modal-close"].onclick = () => close(false);
    elements["confirm-modal-submit"].onclick = () => close(true);
    elements["confirm-modal"].onclick = (event) => { if (event.target === elements["confirm-modal"]) close(false); };
    elements["confirm-modal-cancel"].focus();
  });
}

function applyProviderPreset(provider) {
  selectProviderPill(provider);
  const preset = providerPresets[provider];
  if (!preset) return;
  setServerForm("imap", preset.imap);
  setServerForm("smtp", preset.smtp);
  const email = elements["account-email"].value.trim();
  if (email && !elements["account-username"].value) elements["account-username"].value = email;
  if (!elements["account-name"].value) {
    elements["account-name"].value = provider[0].toUpperCase() + provider.slice(1);
  }
}

function bindEvents() {
  elements["add-account-button"].addEventListener("click", () => openAccountModal());
  elements["refresh-button"].addEventListener("click", () => void loadInbox());
  elements["close-message-button"].addEventListener("click", closeMessage);
  elements["account-modal-close"].addEventListener("click", closeAccountModal);
  elements["cancel-account-button"].addEventListener("click", closeAccountModal);
  elements["account-form"].addEventListener("submit", (event) => void saveAccount(event));
  elements["delete-account-button"].addEventListener("click", () => void deleteCurrentAccount());
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !elements["confirm-modal"].hidden) elements["confirm-modal-cancel"].click();
  });
  elements["account-modal"].addEventListener("click", (event) => {
    if (event.target === elements["account-modal"]) closeAccountModal();
  });
  elements["search-form"].addEventListener("submit", (event) => {
    event.preventDefault();
    const query = elements["mail-search-input"].value.trim();
    if (query) void searchMail(query);
    else {
      updateListHeading();
      void loadInbox();
    }
  });
  elements["account-email"].addEventListener("blur", () => {
    const email = elements["account-email"].value.trim();
    if (email && !elements["account-username"].value) elements["account-username"].value = email;
  });
  document.querySelectorAll("[data-provider]").forEach((button) => {
    button.addEventListener("click", () => applyProviderPreset(button.dataset.provider));
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !elements["account-modal"].hidden) closeAccountModal();
  });
}

function closeMessage() {
  state.selectedMessageKey = null;
  document.querySelector(".mail-shell")?.classList.remove("message-open");
  elements["message-detail"].hidden = true;
  elements["reading-empty"].hidden = false;
  renderMessages();
}

function updateListHeading() {
  const account = state.accounts.find((item) => item.id === state.selectedAccountId);
  const mailbox = state.mailboxes.find((item) => item.id === state.selectedMailboxId);
  elements["message-list-title"].textContent = mailbox?.name || "Inbox";
  elements["message-list-context"].textContent = account ? account.name.toUpperCase() : "ALL ACCOUNTS";
}

function setConnectionState(label, busy) {
  elements["connection-label"].textContent = label;
  elements["sync-caption"].textContent = busy ? "Reading server state" : `${state.accounts.length} account${state.accounts.length === 1 ? "" : "s"} configured`;
  elements["refresh-button"].disabled = busy;
}

function setAccountFormBusy(busy) {
  elements["save-account-button"].disabled = busy;
  elements["delete-account-button"].disabled = busy;
  elements["save-account-button"].textContent = busy ? "Restarting Mail…" : "Save account";
}

function showToast(message, isError = false) {
  clearTimeout(state.toastTimer);
  elements["toast"].textContent = message;
  elements["toast"].classList.toggle("error", isError);
  elements["toast"].hidden = false;
  state.toastTimer = setTimeout(() => {
    elements["toast"].hidden = true;
  }, 4200);
}

function serverForm(kind) {
  const security = elements[`${kind}-security`].value;
  return {
    host: elements[`${kind}-host`].value,
    port: Number(elements[`${kind}-port`].value),
    secure: security === "tls",
    starttls: security === "starttls",
  };
}

function setServerForm(kind, value) {
  elements[`${kind}-host`].value = value.host;
  elements[`${kind}-port`].value = String(value.port);
  elements[`${kind}-security`].value = value.security;
}

function selectProviderPill(provider) {
  document.querySelectorAll("[data-provider]").forEach((button) => {
    button.classList.toggle("active", button.dataset.provider === provider);
  });
  const isGmail = provider === "gmail";
  elements["provider-note"].textContent = isGmail
    ? "Gmail requires a Google App Password. A regular Google account password is rejected by the mail server."
    : "Password and app-password accounts only. OAuth-only providers arrive in a later milestone.";
  elements["account-password-help"].textContent = isGmail
    ? "Create a Google App Password, then paste it here. Leave blank while editing to keep the saved credential."
    : "Stored by the Mail plugin. Leave blank while editing to keep the saved password.";
}

function providerForAccount(account) {
  if (!account) return "custom";
  return Object.entries(providerPresets).find(([, preset]) =>
    preset.imap.host === account.incoming?.host && preset.smtp.host === account.outgoing?.host
  )?.[0] ?? "custom";
}

function folderButton(id, label, icon, count = "") {
  const button = create("button", `folder-item${state.selectedMailboxId === id ? " active" : ""}`);
  button.type = "button";
  button.dataset.folder = id;
  button.append(
    textElement("span", "folder-icon", icon),
    textElement("span", "", label),
    textElement("span", "folder-count", count ? String(count) : ""),
  );
  button.addEventListener("click", () => void selectFolder(id));
  return button;
}

function accountCopy(account) {
  const copy = create("span", "account-copy");
  copy.append(
    textElement("strong", "", account.name),
    textElement("small", "", account.email),
  );
  return copy;
}

function stateDot(enabled) {
  const dot = create("span", `account-state${enabled ? "" : " disabled"}`);
  dot.title = enabled ? "Enabled" : "Disabled";
  return dot;
}

function avatar(name, className) {
  return textElement("span", className, initials(name));
}

function create(tag, className = "") {
  const element = document.createElement(tag);
  if (className) element.className = className;
  return element;
}

function textElement(tag, className, value) {
  const element = create(tag, className);
  element.textContent = value ?? "";
  return element;
}

function senderLabel(sender) {
  if (!sender) return "Unknown sender";
  return sender.name || sender.address || "Unknown sender";
}

function formatAddresses(addresses) {
  return (addresses ?? []).map((item) => item.name || item.address).filter(Boolean).join(", ");
}

function accountName(id) {
  return state.accounts.find((account) => account.id === id)?.name || id;
}

function accountIdFromEmail(email) {
  const local = email.split("@")[0] || "mail";
  const normalized = local.toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
  const taken = new Set(state.accounts.map((account) => account.id));
  if (!taken.has(normalized)) return normalized;
  let suffix = 2;
  while (taken.has(`${normalized}-${suffix}`)) suffix += 1;
  return `${normalized}-${suffix}`;
}

function initials(value) {
  const parts = String(value || "?").trim().split(/\s+/).filter(Boolean);
  return parts.slice(0, 2).map((part) => part[0]).join("").toUpperCase() || "?";
}

function specialUseIcon(value) {
  const key = String(value || "").toLowerCase();
  if (key.includes("sent")) return "↗";
  if (key.includes("draft")) return "✎";
  if (key.includes("trash")) return "⌫";
  if (key.includes("junk")) return "!";
  if (key.includes("archive") || key.includes("all")) return "▣";
  return "□";
}

function shortDate(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const today = new Date();
  if (date.toDateString() === today.toDateString()) {
    return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return date.toLocaleDateString([], { month: "short", day: "numeric" });
}

function longDate(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString([], {
    weekday: "short",
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatBytes(value) {
  const bytes = Number(value) || 0;
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function renderMessageBody(message) {
  const container = elements["detail-body"];
  const body = preferredMailBody(message);
  container.replaceChildren();
  container.classList.toggle("has-html", body.kind === "html");
  if (body.kind === "text") {
    container.textContent = body.value;
    return;
  }

  const frame = document.createElement("iframe");
  frame.className = "mail-html-frame";
  frame.title = `Formatted content for ${message.subject || "email message"}`;
  frame.setAttribute("sandbox", EMAIL_FRAME_SANDBOX);
  frame.referrerPolicy = "no-referrer";
  frame.addEventListener("load", () => {
    const height = frame.contentDocument?.documentElement.scrollHeight;
    if (height) frame.style.height = `${Math.min(4_000, Math.max(220, height))}px`;
  });
  frame.srcdoc = mailFrameDocument(body.value);
  container.append(frame);
}

initialize().catch((error) => {
  renderListState("Mail could not start", readableMailError(error));
  showToast(readableMailError(error), true);
});
