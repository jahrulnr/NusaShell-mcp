const pluginId = new URLSearchParams(location.search).get("pluginId") || "";
const statusEl = document.getElementById("status");
const contentEl = document.getElementById("content");
const sidebarEl = document.getElementById("sidebar");
const searchInput = document.getElementById("searchInput");
const newNoteBtn = document.getElementById("newNoteBtn");
const editorEl = document.getElementById("editor");
const editorTextarea = document.getElementById("editorText");
const editorTags = document.getElementById("editorTags");
const editorSave = document.getElementById("editorSave");
const editorCancel = document.getElementById("editorCancel");
const editorTitle = document.getElementById("editorTitle");
const confirmDialog = document.getElementById("confirmDialog");
const confirmTitle = document.getElementById("confirmTitle");
const confirmMessage = document.getElementById("confirmMessage");
const confirmYes = document.getElementById("confirmYes");
const confirmNo = document.getElementById("confirmNo");

let confirmResolve = null;

function showConfirm(title, message) {
  confirmTitle.textContent = title;
  confirmMessage.textContent = message;
  confirmDialog.classList.add("show");
  return new Promise((resolve) => { confirmResolve = resolve; });
}

function closeConfirm(result) {
  confirmDialog.classList.remove("show");
  if (confirmResolve) { confirmResolve(result); confirmResolve = null; }
}

confirmYes.addEventListener("click", () => closeConfirm(true));
confirmNo.addEventListener("click", () => closeConfirm(false));
confirmDialog.addEventListener("click", (e) => { if (e.target === confirmDialog) closeConfirm(false); });

let allNotes = [];
let allTagsList = [];
let currentFilter = null;
let currentSearch = "";
let editingNoteId = null;

function parseMcpResult(result) {
  if (result == null) return result;

  // The host bridge may return the MCP result unchanged, expose its
  // structuredContent, or wrap it in { requestId, result }. Support every
  // valid form so a host-side transport refactor cannot break the UI.
  const value = result?.structuredContent ?? result?.data ?? result?.result ?? result;

  // If value is still an MCP content array, extract text and JSON.parse it
  if (Array.isArray(value) && value.some((p) => p?.type === "text")) {
    const text = value.find((p) => p?.type === "text")?.text;
    if (typeof text === "string") { try { return JSON.parse(text); } catch { return text; } }
  }
  if (Array.isArray(result) && result.some((p) => p?.type === "text")) {
    const text = result.find((p) => p?.type === "text")?.text;
    if (typeof text === "string") { try { return JSON.parse(text); } catch { return text; } }
  }

  // If value is already a plain object (from structuredContent), return it directly
  if (typeof value === "object" && !Array.isArray(value)) return value;
  return value;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
}

function renderMarkdown(text) {
  try {
    const html = marked.parse(text, { breaks: true, gfm: true });
    const div = document.createElement("div");
    div.innerHTML = html;
    div.querySelectorAll("script").forEach(el => el.remove());
    div.querySelectorAll("*").forEach(el => {
      for (const attr of [...el.attributes]) {
        if (attr.name.startsWith("on")) el.removeAttribute(attr.name);
      }
    });
    return div.innerHTML;
  } catch {
    return escapeHtml(text);
  }
}

function formatDate(iso) {
  const d = new Date(iso);
  const now = new Date();
  const diff = now - d;
  if (diff < 60000) return "just now";
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
  if (diff < 604800000) return `${Math.floor(diff / 86400000)}d ago`;
  return d.toLocaleDateString();
}

function renderSidebar() {
  const total = allNotes.length;
  let html = `
    <div class="sidebar-item ${!currentFilter ? "active" : ""}" data-filter="all">
      <span>📝</span> All Notes
      <span class="count">${total}</span>
    </div>
  `;

  if (allTagsList.length > 0) {
    html += `<div class="sidebar-label">Tags</div>`;
    for (const { tag, count } of allTagsList) {
      html += `
        <div class="sidebar-item ${currentFilter === tag ? "active" : ""}" data-filter="${escapeHtml(tag)}">
          <span>🏷️</span> ${escapeHtml(tag)}
          <span class="count">${count}</span>
        </div>
      `;
    }
  }

  sidebarEl.innerHTML = html;

  sidebarEl.querySelectorAll(".sidebar-item").forEach(el => {
    el.addEventListener("click", () => {
      const filter = el.dataset.filter;
      currentFilter = filter === "all" ? null : filter;
      renderSidebar();
      renderNotes();
    });
  });
}

function getFilteredNotes() {
  let notes = [...allNotes];

  if (currentFilter) {
    notes = notes.filter(n => n.tags && n.tags.includes(currentFilter));
  }

  if (currentSearch) {
    const lower = currentSearch.toLowerCase();
    notes = notes.filter(n =>
      n.text.toLowerCase().includes(lower) ||
      (n.tags || []).some(t => t.toLowerCase().includes(lower))
    );
  }

  return notes.sort((a, b) => new Date(b.updatedAt || b.createdAt) - new Date(a.updatedAt || a.createdAt));
}

function renderNotes() {
  const notes = getFilteredNotes();

  if (notes.length === 0) {
    contentEl.innerHTML = `
      <div id="emptyState">
        <div class="icon">📝</div>
        <div>${allNotes.length === 0 ? "No notes yet. Click New Note to create one." : "No notes match your filter."}</div>
      </div>
    `;
    return;
  }

  contentEl.innerHTML = notes.map(n => {
    const tagsHtml = (n.tags || []).length > 0
      ? `<div class="note-tags">${n.tags.map(t => `<span class="tag-chip" data-tag="${escapeHtml(t)}">${escapeHtml(t)}</span>`).join("")}</div>`
      : "";

    return `
      <div class="note-card" data-id="${n.id}">
        <div class="note-meta">
          <span>#${n.id}</span>
          <span>·</span>
          <span>${formatDate(n.updatedAt || n.createdAt)}</span>
          ${n.updatedAt && n.updatedAt !== n.createdAt ? '<span>· edited</span>' : ''}
        </div>
        ${tagsHtml}
        <div class="note-body">${renderMarkdown(n.text)}</div>
        <div class="note-actions">
          <button data-action="edit" data-id="${n.id}">Edit</button>
          <button class="danger" data-action="delete" data-id="${n.id}">Delete</button>
        </div>
      </div>
    `;
  }).join("");

  contentEl.querySelectorAll(".tag-chip").forEach(el => {
    el.addEventListener("click", (e) => {
      e.stopPropagation();
      currentFilter = el.dataset.tag;
      renderSidebar();
      renderNotes();
    });
  });

  contentEl.querySelectorAll(".note-actions button").forEach(el => {
    el.addEventListener("click", (e) => {
      e.stopPropagation();
      const action = el.dataset.action;
      const id = Number(el.dataset.id);
      if (action === "edit") openEditor(id);
      if (action === "delete") deleteNote(id);
    });
  });
}

async function refreshAll() {
  try {
    const result = await window.shell.callTool(pluginId, "list", { sort: "updated" });
    const data = parseMcpResult(result);
    allNotes = data.notes || [];

    const tagCounts = {};
    for (const note of allNotes) {
      for (const tag of (note.tags || [])) {
        tagCounts[tag] = (tagCounts[tag] || 0) + 1;
      }
    }
    allTagsList = Object.entries(tagCounts)
      .map(([tag, count]) => ({ tag, count }))
      .sort((a, b) => b.count - a.count);

    renderSidebar();
    renderNotes();
  } catch (err) {
    statusEl.textContent = "Failed to load notes: " + err.message;
  }
}

function openEditor(id = null) {
  editingNoteId = id;
  if (id) {
    const note = allNotes.find(n => n.id === id);
    if (!note) return;
    editorTitle.textContent = "Edit Note";
    editorTextarea.value = note.text;
    editorTags.value = (note.tags || []).join(", ");
  } else {
    editorTitle.textContent = "New Note";
    editorTextarea.value = "";
    editorTags.value = "";
  }
  editorEl.classList.add("show");
  editorTextarea.focus();
}

function closeEditor() {
  editorEl.classList.remove("show");
  editingNoteId = null;
}

async function saveNote() {
  const text = editorTextarea.value.trim();
  if (!text) return;
  const tags = editorTags.value.split(",").map(t => t.trim()).filter(Boolean);

  try {
    if (editingNoteId) {
      await window.shell.callTool(pluginId, "update", { id: editingNoteId, text, tags });
      statusEl.textContent = `Note #${editingNoteId} updated`;
    } else {
      const result = await window.shell.callTool(pluginId, "create", { text, tags });
      const data = parseMcpResult(result);
      statusEl.textContent = `Note #${data.note.id} created`;
    }
    closeEditor();
    await refreshAll();
  } catch (err) {
    statusEl.textContent = "Error: " + err.message;
  }
}

async function deleteNote(id) {
  const confirmed = await showConfirm("Delete note?", "This action cannot be undone. The note will be permanently removed.");
  if (!confirmed) return;
  try {
    await window.shell.callTool(pluginId, "delete", { id });
    statusEl.textContent = `Note #${id} deleted`;
    await refreshAll();
  } catch (err) {
    statusEl.textContent = "Error: " + err.message;
  }
}

newNoteBtn.addEventListener("click", () => openEditor());

editorSave.addEventListener("click", saveNote);
editorCancel.addEventListener("click", closeEditor);

editorTextarea.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) saveNote();
  if (e.key === "Escape") closeEditor();
});

searchInput.addEventListener("input", () => {
  currentSearch = searchInput.value.trim();
  renderNotes();
});

refreshAll().catch((err) => {
  statusEl.textContent = "Failed to load notes: " + (err?.message || err);
});
