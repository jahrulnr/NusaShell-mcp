import { formatSize, formatDate, buildBreadcrumbs, joinPath, parentPath } from "./files-ui-state.js";

const pluginId = new URLSearchParams(location.search).get("pluginId") || "nusashell.files";
const state = { currentPath: "/", items: [], selectedItem: null, tree: null, expandedDirs: new Set(["/"]), searchQuery: "", searchResults: null };
const elements = {};
let dialog = null;
let modalResolve = null;

const ICONS = {
  folder: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6.8A1.8 1.8 0 0 1 4.8 5h4l1.7 2h8.7A1.8 1.8 0 0 1 21 8.8v8.4a1.8 1.8 0 0 1-1.8 1.8H4.8A1.8 1.8 0 0 1 3 17.2Z"/></svg>',
  image: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18.5Z"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="m5 17 4.5-4.5 3 3L15 13l4 4"/></svg>',
  archive: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h14v16H5z"/><path d="M9 4v16M8 8h2M8 12h2M8 16h2"/></svg>',
  code: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18-6-6 6-6M15 6l6 6-6 6"/></svg>',
  file: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3.5h8l4 4v13H6z"/><path d="M14 3.5v4h4M9 12h6M9 15h5"/></svg>',
  open: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h13M14 7l5 5-5 5"/></svg>',
};

function $(id) { return document.getElementById(id); }
function displayPath(path) { if (!path || path === "/") return "/"; return `/${String(path).replace(/^\/+/, "")}`; }
function toolPath(path) {
  // The Files MCP server requires ABSOLUTE paths (resolvePath rejects
  // relative paths because the server is shared between concurrent
  // agents and a relative path has no stable meaning across them).
  // state.currentPath and item.path are always display paths with a
  // leading slash; preserve it so the server receives an absolute path.
  if (!path || path === "/") return "/";
  const stripped = String(path).replace(/^\/+/, "");
  return "/" + stripped;
}
function itemPath(item) { return displayPath(item.path || joinPath(state.currentPath, item.name)); }
function iconFor(item) { if (item.isDir) return { key: "folder", className: "folder" }; if (item.type === "image") return { key: "image", className: "image" }; if (item.type === "archive") return { key: "archive", className: "archive" }; if (item.type === "text") return { key: "code", className: "code" }; return { key: "file", className: "file" }; }

function initElements() {
  for (const id of ["files-workspace", "root-caption", "root-button", "search-form", "search-input", "refresh-button", "up-button", "new-file-button", "upload-button", "upload-input", "tree-container", "collapse-tree-button", "breadcrumbs", "folder-title", "listing-count", "listing-body", "new-folder-button", "selection-actions", "rename-button", "delete-button", "preview-pane", "preview-icon", "preview-title", "preview-subtitle", "preview-meta", "preview-content", "close-preview-button", "files-drop-overlay", "modal-overlay", "modal-title", "modal-description", "modal-form", "modal-name-field", "modal-input", "modal-textarea", "modal-content-field", "modal-field-label", "confirm-copy", "modal-error", "modal-cancel-button", "modal-save-button", "modal-close-button", "toast-container"]) elements[id] = $(id);
}

async function callTool(name, args = {}) {
  if (!window.shell || typeof window.shell.callTool !== "function") {
    throw new Error("NusaShell's plugin bridge is unavailable. Open Files from the NusaShell launcher.");
  }

  const result = await window.shell.callTool(pluginId, name, args);
  if (result == null) {
    throw new Error(`Files did not receive a response for ${name}. Restart the Files plugin and try again.`);
  }
  if (result?.isError || result?.ok === false) {
    throw new Error(toolErrorMessage(result) ?? "The request could not be completed.");
  }

  // The host bridge may return the MCP result unchanged, expose its
  // structuredContent, or pass the tool value directly. Support every valid
  // form so a host-side transport refactor cannot turn a healthy MCP response
  // into an undefined renderer value.
  const value = result?.structuredContent ?? result?.data ?? result?.result ?? result;
  if (Array.isArray(value) && value.some((p) => p?.type === "text")) {
    const text = value.find((p) => p?.type === "text")?.text;
    if (typeof text === "string") { try { return JSON.parse(text); } catch { return text; } }
  }
  if (value?.content && !value?.items && !value?.tree && !value?.results && !value?.path) {
    const text = value.content.find((part) => part?.type === "text")?.text;
    if (typeof text === "string") {
      try { return JSON.parse(text); } catch { return text; }
    }
  }
  return value;
}

function toolErrorMessage(result) {
  if (typeof result?.error === "string") return result.error;
  const text = result?.content?.find((part) => part?.type === "text")?.text;
  return typeof text === "string" ? text : null;
}

function expectArray(value, field, toolName) {
  if (Array.isArray(value?.[field])) return value[field];
  throw new Error(`Files returned an invalid ${field} response from ${toolName}. Restart the plugin and try again.`);
}

function toast(message, type = "") { const el = document.createElement("div"); el.className = `toast ${type}`; el.textContent = message; elements["toast-container"].append(el); setTimeout(() => el.remove(), 3200); }
function setListingMessage(message) { elements["listing-body"].replaceChildren(Object.assign(document.createElement("p"), { className: "listing-empty", textContent: message })); }

async function loadListing() {
  setListingMessage("Loading files…");
  try {
    const data = await callTool("list", { path: toolPath(state.currentPath) });
    state.items = expectArray(data, "items", "list"); state.selectedItem = null;
    renderListing(); renderBreadcrumbs(); updateActions(); closePreview();
  } catch (error) { setListingMessage(error.message); elements["listing-count"].textContent = "Unavailable"; }
}
async function loadTree() { try { const data = await callTool("tree", { path: "/", depth: 3 }); state.tree = expectArray(data, "tree", "tree"); renderTree(); } catch { elements["tree-container"].innerHTML = '<p class="tree-empty">Folders are unavailable</p>'; } }

function renderBreadcrumbs() {
  const crumbs = buildBreadcrumbs(state.currentPath);
  const frag = document.createDocumentFragment();
  for (const crumb of crumbs) { const button = document.createElement("button"); button.type = "button"; button.className = `breadcrumb-item${crumb.current ? " current" : ""}`; button.textContent = crumb.name; button.addEventListener("click", () => navigateTo(crumb.path)); frag.append(button); if (!crumb.current) { const sep = document.createElement("span"); sep.className = "breadcrumb-sep"; sep.textContent = "›"; frag.append(sep); } }
  elements["breadcrumbs"].replaceChildren(frag);
  elements["folder-title"].textContent = crumbs.at(-1)?.name ?? "Home";
  elements["root-button"].classList.toggle("active", state.currentPath === "/");
}

function listingItem(item) {
  const row = document.createElement("div"); row.className = "listing-item"; row.setAttribute("role", "listitem"); row.tabIndex = 0; if (state.selectedItem?.path === item.path) row.classList.add("selected");
  const icon = iconFor(item); const name = document.createElement("div"); name.className = "listing-name"; const glyph = document.createElement("span"); glyph.className = `listing-item-icon ${icon.className}`; glyph.innerHTML = ICONS[icon.key]; const label = document.createElement("span"); label.className = "listing-item-name"; label.textContent = item.name; name.append(glyph, label);
  const size = document.createElement("span"); size.className = "listing-item-size"; size.textContent = item.isDir ? "—" : formatSize(item.size);
  const modified = document.createElement("span"); modified.className = "listing-item-modified"; modified.textContent = formatDate(item.modified);
  const open = document.createElement("button"); open.type = "button"; open.className = "row-open"; open.title = item.isDir ? "Open folder" : "View details"; open.setAttribute("aria-label", open.title); open.innerHTML = ICONS.open; open.addEventListener("click", (event) => { event.stopPropagation(); openItem(item); });
  row.append(name, size, modified, open);
  row.addEventListener("click", () => selectItem(item)); row.addEventListener("dblclick", () => openItem(item)); row.addEventListener("keydown", (event) => { if (event.key === "Enter") openItem(item); if (event.key === " ") { event.preventDefault(); selectItem(item); } });
  return row;
}

function renderListing() {
  const items = state.searchResults ?? state.items; const isSearch = Boolean(state.searchQuery);
  if (!Array.isArray(items) || !items.length) { setListingMessage(isSearch ? "No matching files in this workspace." : "This folder is empty. Create a file or folder to get started."); elements["listing-count"].textContent = isSearch ? "0 results" : "Empty folder"; return; }
  elements["listing-count"].textContent = isSearch ? `${items.length} result${items.length === 1 ? "" : "s"} for “${state.searchQuery}”` : `${items.length} item${items.length === 1 ? "" : "s"}`;
  const frag = document.createDocumentFragment(); for (const item of items) frag.append(listingItem(item)); elements["listing-body"].replaceChildren(frag);
}

function renderTree() { if (!state.tree) return; elements["tree-container"].replaceChildren(renderTreeNodes(state.tree)); }
function renderTreeNodes(nodes) { const frag = document.createDocumentFragment(); for (const node of nodes) { if (!node.isDir) continue; const path = displayPath(node.path); const expanded = state.expandedDirs.has(path); const row = document.createElement("button"); row.type = "button"; row.className = `tree-node${state.currentPath === path ? " active" : ""}`; row.innerHTML = `<span class="tree-toggle${expanded ? " expanded" : ""}">▶</span><span class="tree-icon">${ICONS.folder}</span><span class="tree-label"></span>`; row.querySelector(".tree-label").textContent = node.name; row.addEventListener("click", () => { expanded ? state.expandedDirs.delete(path) : state.expandedDirs.add(path); navigateTo(path); }); frag.append(row); if (expanded && node.children) { const children = document.createElement("div"); children.className = "tree-children"; children.append(renderTreeNodes(node.children)); frag.append(children); } } return frag; }

function selectItem(item) { state.selectedItem = item; renderListing(); updateActions(); }
function updateActions() { elements["selection-actions"].hidden = !state.selectedItem; }
function navigateTo(path) { state.currentPath = displayPath(path); state.searchQuery = ""; state.searchResults = null; elements["search-input"].value = ""; loadListing(); renderTree(); }
function openItem(item) { if (item.isDir) { navigateTo(itemPath(item)); } else { openPreview(item); } }

async function openPreview(item) {
  const path = itemPath(item); state.selectedItem = item; updateActions(); renderListing(); elements["preview-pane"].hidden = false; elements["files-workspace"].classList.add("preview-open");
  const icon = iconFor(item); elements["preview-icon"].className = `preview-file-icon ${icon.className}`; elements["preview-icon"].innerHTML = ICONS[icon.key]; elements["preview-title"].textContent = item.name; elements["preview-subtitle"].textContent = item.type === "text" ? "Text file" : item.type === "image" ? "Image" : "File"; elements["preview-content"].className = "preview-content"; elements["preview-content"].textContent = "Loading preview…";
  try {
    const info = await callTool("info", { path: toolPath(path) });
    renderPreviewMeta(info);
    if (item.type === "text") { const result = await callTool("read", { path: toolPath(path) }); elements["preview-content"].textContent = result.content || "This file is empty."; }
    else if (item.type === "image") { elements["preview-content"].className = "preview-content binary-note"; elements["preview-content"].textContent = "Image previews are not available in the plugin sandbox."; }
    else { elements["preview-content"].className = "preview-content binary-note"; elements["preview-content"].textContent = `Preview is not available for ${item.type} files.`; }
  } catch (error) { elements["preview-content"].className = "preview-content binary-note"; elements["preview-content"].textContent = error.message; }
}
function renderPreviewMeta(info) { const fields = [["Location", displayPath(info.path)], ["Size", info.isDir ? "Folder" : formatSize(info.size)], ["Modified", formatDate(info.modified)], ["Permissions", info.permissions]]; const frag = document.createDocumentFragment(); for (const [label, value] of fields) { const row = document.createElement("div"); row.className = "preview-meta-row"; const left = document.createElement("span"); left.textContent = label; const right = document.createElement("span"); right.textContent = value; row.append(left, right); frag.append(row); } elements["preview-meta"].replaceChildren(frag); }
function closePreview() { elements["preview-pane"].hidden = true; elements["files-workspace"].classList.remove("preview-open"); }

function openModal(config) { dialog = config; elements["modal-title"].textContent = config.title; elements["modal-description"].textContent = config.description ?? ""; elements["modal-field-label"].textContent = config.fieldLabel ?? "Name"; elements["modal-input"].value = config.defaultValue ?? ""; elements["modal-input"].required = !config.confirm; elements["modal-name-field"].hidden = Boolean(config.confirm); elements["modal-content-field"].hidden = !config.showContent; elements["modal-textarea"].value = ""; elements["confirm-copy"].hidden = !config.confirm; elements["confirm-copy"].replaceChildren(); if (config.confirm) { const strong = document.createElement("strong"); strong.textContent = config.confirm; elements["confirm-copy"].append("This will permanently delete ", strong, ". This action cannot be undone."); } elements["modal-save-button"].textContent = config.submitLabel ?? "Save"; elements["modal-save-button"].classList.toggle("danger", Boolean(config.danger)); elements["modal-error"].textContent = ""; elements["modal-overlay"].hidden = false; setTimeout(() => (config.confirm ? elements["modal-save-button"] : elements["modal-input"]).focus(), 0); return new Promise(resolve => { modalResolve = resolve; }); }
function closeModal(result) { elements["modal-overlay"].hidden = true; if (modalResolve) { modalResolve(result); modalResolve = null; } dialog = null; }
async function handleNewFile() { const value = await openModal({ title:"Create file", description:"A new text file will be created in the current folder.", fieldLabel:"File name", showContent:true, submitLabel:"Create file" }); if (!value) return; try { await callTool("write", { path: toolPath(joinPath(state.currentPath, value.name)), content:value.content }); toast("File created", "success"); refresh(); } catch (error) { toast(error.message, "error"); } }
async function handleNewFolder() { const value = await openModal({ title:"Create folder", description:"Folders can be empty and can contain other folders.", fieldLabel:"Folder name", submitLabel:"Create folder" }); if (!value) return; try { await callTool("mkdir", { path: toolPath(joinPath(state.currentPath, value.name)) }); toast("Folder created", "success"); refresh(); } catch (error) { toast(error.message, "error"); } }
async function handleRename() { if (!state.selectedItem) return; const item = state.selectedItem; const value = await openModal({ title:"Rename", description:"The item stays in the same folder.", fieldLabel:"New name", defaultValue:item.name, submitLabel:"Rename" }); if (!value || value.name === item.name) return; try { await callTool("move", { source:toolPath(itemPath(item)), destination:toolPath(joinPath(state.currentPath, value.name)) }); toast("Renamed", "success"); refresh(); } catch (error) { toast(error.message, "error"); } }
async function handleDelete() { if (!state.selectedItem) return; const item = state.selectedItem; const confirmed = await openModal({ title:"Delete item?", description:"Please review this destructive action.", confirm:item.name, submitLabel:"Delete", danger:true }); if (!confirmed) return; try { await callTool("delete", { path:toolPath(itemPath(item)), recursive:item.isDir }); toast("Deleted", "success"); refresh(); } catch (error) { toast(error.message, "error"); } }
async function handleSearch(query) { const clean = query.trim(); if (!clean) { state.searchQuery=""; state.searchResults=null; renderListing(); return; } state.searchQuery=clean; try { const result=await callTool("search", { path:toolPath(state.currentPath), pattern:clean.includes("*") || clean.includes("?") ? clean : `*${clean}*` }); state.searchResults=expectArray(result, "results", "search"); state.selectedItem=null; renderListing(); updateActions(); } catch(error) { toast(error.message,"error"); } }
function refresh() { loadListing(); loadTree(); }
function goUp() { const parent=parentPath(state.currentPath); if (parent !== state.currentPath) navigateTo(parent); } function collapseTree() { state.expandedDirs=new Set(["/"]); renderTree(); }

// ===== Upload & drop (ticket #77) =====

async function bytesToBase64(bytes) {
  // Chunked to avoid call-stack overflow for large files (btoa is synchronous
  // and per-char; chunking keeps the loop balanced).
  const CHUNK = 0x8000;
  let binary = "";
  for (let i = 0; i < bytes.length; i += CHUNK) {
    binary += String.fromCharCode(...bytes.subarray(i, i + CHUNK));
  }
  return btoa(binary);
}

function decodeUtf8Strict(bytes) {
  if (bytes.length === 0) return "";
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return null;
  }
}

async function uploadFile(file, rel) {
  const targetName = rel || file.name || "upload.bin";
  const targetPath = toolPath(joinPath(state.currentPath, targetName));
  const bytes = new Uint8Array(await file.arrayBuffer());
  // Text files are stored as UTF-8 text so they stay previewable/editable;
  // binary content is base64-encoded and written byte-for-byte.
  const text = decodeUtf8Strict(bytes);
  if (text !== null) {
    await callTool("write", { path: targetPath, content: text });
  } else {
    const base64 = await bytesToBase64(bytes);
    await callTool("write", { path: targetPath, content: base64, encoding: "base64" });
  }
  return targetName;
}

async function handleUpload(fileList) {
  const files = [...(fileList ?? [])];
  if (files.length === 0) return;
  const names = [];
  let failed = 0;
  for (const file of files) {
    if (file.size > 10 * 1024 * 1024) {
      toast(`${file.name} is larger than 10 MiB and was skipped.`, "error");
      failed += 1;
      continue;
    }
    try {
      const name = await uploadFile(file);
      names.push(name);
    } catch (error) {
      toast(`Could not upload ${file.name}: ${error.message}`, "error");
      failed += 1;
    }
  }
  if (names.length) toast(`${names.length} file${names.length === 1 ? "" : "s"} uploaded`, "success");
  if (failed) toast(`${failed} file${failed === 1 ? "" : "s"} failed`, "error");
  if (names.length) refresh();
}

// ===== Dropped-folder traversal (ticket #76) =====

function entryToFile(entry) {
  return new Promise((resolve) => entry.file(resolve, () => resolve(null)));
}

/**
 * Recursively read a dropped directory via webkitGetAsEntry and collect its
 * files. readEntries returns at most 100 entries per call, so batches are
 * drained until empty (Chromium convention). The relative path under the
 * dropped folder is preserved using the entry name chain.
 * @returns {Promise<Array<{file: File, rel: string}>>}
 */
async function flattenDroppedDir(entry, baseRel, out) {
  if (entry.isFile) {
    const file = await entryToFile(entry);
    if (file) out.push({ file, rel: baseRel });
    return;
  }
  if (!entry.isDirectory) return;
  const reader = entry.createReader();
  for (;;) {
    const batch = await new Promise((resolve) => reader.readEntries(resolve, () => resolve([])));
    if (!batch.length) break;
    for (const child of batch) {
      const childRel = `${baseRel}/${child.name}`.replace(/^\/\/?/, "");
      await flattenDroppedDir(child, childRel, out);
    }
  }
}

async function readDroppedFolders(items) {
  const out = [];
  for (const item of items) {
    const entry = item.webkitGetAsEntry?.();
    if (!entry) continue;
    if (entry.isDirectory) await flattenDroppedDir(entry, "", out);
    else {
      const file = await entryToFile(entry);
      if (file) out.push({ file, rel: file.name });
    }
  }
  return out;
}

async function handleDroppedFolder(items) {
  const entries = await readDroppedFolders(items);
  if (entries.length === 0) {
    toast("The dropped folder appears to be empty or unreadable.", "error");
    return;
  }
  let uploaded = 0;
  let failed = 0;
  for (const { file, rel } of entries) {
    if (file.size > 10 * 1024 * 1024) {
      toast(`${file.name} is larger than 10 MiB and was skipped.`, "error");
      failed += 1;
      continue;
    }
    try {
      await uploadFile(file, rel);
      uploaded += 1;
    } catch (error) {
      toast(`Could not upload ${file.name}: ${error.message}`, "error");
      failed += 1;
    }
  }
  if (uploaded) toast(`${uploaded} file${uploaded === 1 ? "" : "s"} uploaded from the dropped folder`, "success");
  if (failed) toast(`${failed} file${failed === 1 ? "" : "s"} failed`, "error");
  if (uploaded) refresh();
}

function dragDepth() { return document.getElementById("files-drop-overlay")?.dataset.depth ? Number(document.getElementById("files-drop-overlay").dataset.depth) : 0; }
function setDragDepth(value) { const el = document.getElementById("files-drop-overlay"); if (el) el.dataset.depth = String(value); }
function showDropOverlay(label) { const el = document.getElementById("files-drop-overlay"); if (!el) return; el.querySelector(".files-drop-label").textContent = label; el.hidden = false; }
function hideDropOverlay() { const el = document.getElementById("files-drop-overlay"); if (el) { el.hidden = true; el.dataset.depth = "0"; } }

function isFileDrag(event) {
  const types = event.dataTransfer?.types;
  return Boolean(types && [...types].some((t) => t === "Files" || t === "application/x-moz-file"));
}

async function handleFilesDrop(event) {
  hideDropOverlay();
  const items = [...(event.dataTransfer?.items ?? [])];
  const files = [...(event.dataTransfer?.files ?? [])];

  // Folder drops: Chromium gives an empty FileList but DataTransferItems with
  // webkitGetAsEntry. Traverse the dropped directory and upload its contents,
  // preserving relative paths (ticket #76).
  if (files.length === 0 && items.length > 0) {
    const hasDirEntry = items.some((item) => item.webkitGetAsEntry?.()?.isDirectory);
    if (hasDirEntry) {
      await handleDroppedFolder(items);
      return;
    }
    const entryFiles = [];
    for (const item of items) {
      const entry = item.webkitGetAsEntry?.();
      if (!entry) continue;
      const file = await entryToFile(entry);
      if (file) entryFiles.push(file);
    }
    if (entryFiles.length > 0) {
      await handleUpload(entryFiles);
      return;
    }
    toast("This surface cannot open the dropped item. Use New folder or navigate with the tree.", "error");
    return;
  }

  if (files.length === 0) return;
  await handleUpload(files);
}

function initDropZone() {
  const overlay = document.getElementById("files-drop-overlay");
  if (!overlay) return;

  const onDragEnter = (event) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    setDragDepth(dragDepth() + 1);
    showDropOverlay("Drop to upload into this folder");
  };
  const onDragOver = (event) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
  };
  const onDragLeave = (event) => {
    if (!isFileDrag(event)) return;
    setDragDepth(Math.max(0, dragDepth() - 1));
    if (dragDepth() === 0) hideDropOverlay();
  };
  const onDrop = (event) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    void handleFilesDrop(event);
  };
  const onBlur = () => hideDropOverlay();

  document.addEventListener("dragenter", onDragEnter);
  document.addEventListener("dragover", onDragOver);
  document.addEventListener("dragleave", onDragLeave);
  document.addEventListener("drop", onDrop);
  window.addEventListener("blur", onBlur);
}

function init() {
  initElements(); elements["root-caption"].textContent="Home";
  elements["search-form"].addEventListener("submit", e => { e.preventDefault(); handleSearch(elements["search-input"].value); }); elements["search-input"].addEventListener("search", () => { if (!elements["search-input"].value) handleSearch(""); });
  elements["refresh-button"].addEventListener("click", refresh); elements["up-button"].addEventListener("click", goUp); elements["root-button"].addEventListener("click", () => navigateTo("/")); elements["new-file-button"].addEventListener("click", handleNewFile); elements["new-folder-button"].addEventListener("click", handleNewFolder); elements["rename-button"].addEventListener("click", handleRename); elements["delete-button"].addEventListener("click", handleDelete); elements["close-preview-button"].addEventListener("click", closePreview); elements["collapse-tree-button"].addEventListener("click", collapseTree);
  elements["upload-button"].addEventListener("click", () => elements["upload-input"].click());
  elements["upload-input"].addEventListener("change", (event) => { void handleUpload(event.target.files); event.target.value = ""; });
  initDropZone();
  elements["modal-form"].addEventListener("submit", e => { e.preventDefault(); if (dialog?.confirm) return closeModal({ confirmed:true }); const name=elements["modal-input"].value.trim(); if (!name) { elements["modal-error"].textContent="A name is required."; return; } closeModal({ name, content:elements["modal-textarea"].value }); }); elements["modal-cancel-button"].addEventListener("click", () => closeModal(null)); elements["modal-close-button"].addEventListener("click", () => closeModal(null)); elements["modal-overlay"].addEventListener("click", e => { if(e.target === elements["modal-overlay"]) closeModal(null); });
  document.addEventListener("keydown", e => { if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase()==="k") { e.preventDefault(); elements["search-input"].focus(); } if(e.key === "Escape") { if(!elements["modal-overlay"].hidden) closeModal(null); else if(!elements["preview-pane"].hidden) closePreview(); } });
  refresh();
}
init();

export { handleUpload, bytesToBase64, initDropZone, handleFilesDrop, handleDroppedFolder, readDroppedFolders };
