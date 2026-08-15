/**
 * UI state helpers for the Files plugin.
 * Pure functions for formatting, breadcrumb building, and icon mapping.
 */

const FOLDER_ICON = "📁";
const FILE_ICONS = {
  text: "📄",
  image: "🖼️",
  pdf: "📕",
  video: "🎬",
  audio: "🎵",
  archive: "📦",
  binary: "⚙️",
};

export function fileIcon(item) {
  if (item.isDir) return FOLDER_ICON;
  return FILE_ICONS[item.type] ?? FILE_ICONS.binary;
}

export function formatSize(bytes) {
  if (!bytes || bytes === 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

export function formatDate(isoString) {
  if (!isoString) return "—";
  const d = new Date(isoString);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay) {
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

export function buildBreadcrumbs(currentPath, rootLabel = "Home") {
  if (!currentPath || currentPath === "/") {
    return [{ name: rootLabel, path: "/", current: true }];
  }
  const parts = currentPath.split("/").filter(Boolean);
  const crumbs = [{ name: rootLabel, path: "/" }];
  let accumulated = "";
  for (let i = 0; i < parts.length; i++) {
    accumulated += "/" + parts[i];
    crumbs.push({
      name: parts[i],
      path: accumulated,
      current: i === parts.length - 1,
    });
  }
  return crumbs;
}

export function joinPath(base, name) {
  if (!base || base === "/") return "/" + name;
  return base.endsWith("/") ? base + name : base + "/" + name;
}

export function parentPath(path) {
  if (!path || path === "/") return "/";
  const parts = path.split("/").filter(Boolean);
  parts.pop();
  return parts.length === 0 ? "/" : "/" + parts.join("/");
}

export function baseName(path) {
  if (!path || path === "/") return "";
  const parts = path.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? "";
}
