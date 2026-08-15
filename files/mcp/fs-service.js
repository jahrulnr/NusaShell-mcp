import fs from "node:fs/promises";
import path from "node:path";
import { resolvePath, validateRoot, relativePosix, splitLines } from "./config.js";

const MAX_READ_BYTES = 10 * 1024 * 1024;
const MAX_TREE_DEPTH = 10;
const MAX_SEARCH_RESULTS = 1000;
const MAX_GREP_LINE_LENGTH = 500;
/** Number of bytes to sniff for magic-byte detection. */
const MAGIC_BYTE_SAMPLE = 512;
/** Max ratio of NUL bytes to consider a file text (heuristic). */
const BINARY_NUL_RATIO = 0.30;

const TEXT_EXTENSIONS = new Set([
  ".txt", ".md", ".markdown", ".json", ".js", ".mjs", ".cjs", ".ts", ".tsx",
  ".jsx", ".html", ".htm", ".css", ".scss", ".less", ".xml", ".yaml", ".yml",
  ".toml", ".ini", ".cfg", ".conf", ".env", ".gitignore", ".svg", ".csv",
  ".log", ".sh", ".bash", ".zsh", ".fish", ".py", ".rb", ".go", ".rs",
  ".java", ".kt", ".swift", ".c", ".cpp", ".h", ".hpp", ".cs", ".php",
  ".pl", ".lua", ".r", ".sql", ".graphql", ".gql", ".vue", ".svelte",
  ".mod", ".sum", ".lock", ".work", ".txt",
]);

const IMAGE_EXTENSIONS = new Set([
  ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".ico", ".tiff", ".tif",
  ".avif", ".heic", ".heif",
]);

/** Files with no extension (or unknown extension) that are commonly text. */
const TEXT_BASENAMES = new Set([
  "makefile", "dockerfile", "license", "readme", "changelog", "authors",
  "contributors", "todo", "notice", ".gitignore", ".gitattributes",
  ".editorconfig", ".npmrc", ".env", ".env.local", ".env.production",
  "go.mod", "go.sum", "go.work", "go.work.sum",
  ".bashrc", ".bash_profile", ".profile", ".zshrc",
  "procfile", "gemfile", "rakefile", "vagrantfile",
  ".dockerignore", ".prettierrc", ".eslintrc", ".babelrc",
]);

/** Magic byte signatures for known binary types. */
const MAGIC_BYTES = [
  { offset: 0, bytes: [0x89, 0x50, 0x4e, 0x47], type: "image" },           // PNG
  { offset: 0, bytes: [0xff, 0xd8, 0xff], type: "image" },                 // JPEG
  { offset: 0, bytes: [0x47, 0x49, 0x46, 0x38], type: "image" },           // GIF
  { offset: 0, bytes: [0x42, 0x4d], type: "image" },                       // BMP
  { offset: 0, bytes: [0x25, 0x50, 0x44, 0x46], type: "pdf" },             // PDF
  { offset: 0, bytes: [0x50, 0x4b, 0x03, 0x04], type: "archive" },         // ZIP
  { offset: 0, bytes: [0x1f, 0x8b], type: "archive" },                     // GZIP
  { offset: 257, bytes: [0x75, 0x73, 0x74, 0x61, 0x72], type: "archive" }, // TAR
  { offset: 0, bytes: [0x52, 0x61, 0x72, 0x21], type: "archive" },         // RAR
  { offset: 0, bytes: [0x37, 0x7a, 0xbc, 0xaf], type: "archive" },         // 7Z
  { offset: 0, bytes: [0x00, 0x00, 0x01, 0xba], type: "video" },           // MPEG
  { offset: 0, bytes: [0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70], type: "video" }, // MP4
  { offset: 0, bytes: [0x49, 0x44, 0x33], type: "audio" },                 // MP3
  { offset: 0, bytes: [0x52, 0x49, 0x46, 0x46], type: "audio" },           // WAV/AVI (check further)
];

/**
 * Detect file type by extension (fast path). Returns null if unknown.
 * @param {string} name
 * @returns {string|null}
 */
function detectByExtension(name) {
  const ext = path.extname(name).toLowerCase();
  const lower = name.toLowerCase();
  if (TEXT_EXTENSIONS.has(ext)) return "text";
  if (IMAGE_EXTENSIONS.has(ext)) return "image";
  if (ext === ".pdf") return "pdf";
  if ([".mp4", ".webm", ".avi", ".mov", ".mkv"].includes(ext)) return "video";
  if ([".mp3", ".wav", ".ogg", ".flac", ".m4a"].includes(ext)) return "audio";
  if ([".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar"].includes(ext)) return "archive";
  // Check basename for extensionless text files (go.mod, Makefile, etc.)
  const basename = path.basename(lower);
  if (TEXT_BASENAMES.has(basename)) return "text";
  if (TEXT_BASENAMES.has(ext)) return "text";
  return null;
}

/**
 * Check if a buffer looks like text by counting NUL bytes and non-printable
 * characters in the first N bytes. This is the same heuristic used by `file`
 * and `grep --binary-files`.
 * @param {Buffer} buf
 * @returns {boolean}
 */
function isTextBuffer(buf) {
  if (buf.length === 0) return true;
  const sample = buf.subarray(0, Math.min(buf.length, MAGIC_BYTE_SAMPLE));
  let nulCount = 0;
  for (let i = 0; i < sample.length; i++) {
    if (sample[i] === 0) nulCount++;
  }
  // High ratio of NUL bytes → binary.
  if (nulCount / sample.length > BINARY_NUL_RATIO) return false;
  // Check for UTF-8 BOM.
  if (sample.length >= 3 && sample[0] === 0xef && sample[1] === 0xbb && sample[2] === 0xbf) return true;
  // Check for common control characters that indicate binary.
  let controlCount = 0;
  for (let i = 0; i < sample.length; i++) {
    const byte = sample[i];
    // Allow: tab (9), LF (10), CR (13), escape (27), and all printable (32-126).
    // Also allow high bytes (128+) for UTF-8 multibyte sequences.
    if (byte === 0 || (byte < 32 && byte !== 9 && byte !== 10 && byte !== 13 && byte !== 27)) {
      controlCount++;
    }
  }
  return controlCount / sample.length < 0.10;
}

/**
 * Check magic byte signatures against a buffer.
 * @param {Buffer} buf
 * @returns {string|null}
 */
function detectByMagicBytes(buf) {
  for (const sig of MAGIC_BYTES) {
    if (buf.length < sig.offset + sig.bytes.length) continue;
    let match = true;
    for (let i = 0; i < sig.bytes.length; i++) {
      if (buf[sig.offset + i] !== sig.bytes[i]) { match = false; break; }
    }
    if (match) return sig.type;
  }
  return null;
}

/**
 * Detect file type by extension (fast path) and magic bytes (authoritative).
 * For read, the caller should use isTextFile() which reads the actual
 * content. This function is for info / list where reading content
 * may be too expensive.
 * @param {string} name
 */
export function detectFileType(name) {
  const byExt = detectByExtension(name);
  if (byExt) return byExt;
  return "binary";
}

/**
 * Authoritative text detection: reads the first N bytes and checks magic bytes
 * + NUL/control character ratio. Falls back to extension hint if the file is
 * empty or unreadable.
 * @param {string} filePath
 * @returns {Promise<{type: string, isText: boolean}>}
 */
export async function detectFileTypeByContent(filePath) {
  const byExt = detectByExtension(filePath);
  try {
    const fd = await fs.open(filePath, "r");
    try {
      const buf = Buffer.alloc(MAGIC_BYTE_SAMPLE);
      const { bytesRead } = await fd.read(buf, 0, MAGIC_BYTE_SAMPLE, 0);
      const sample = buf.subarray(0, bytesRead);
      // Magic bytes take priority for known binary types.
      const byMagic = detectByMagicBytes(sample);
      if (byMagic) return { type: byMagic, isText: false };
      // No magic byte match — check if it looks like text.
      if (isTextBuffer(sample)) return { type: "text", isText: true };
      // Not text, no known magic bytes.
      return { type: byExt ?? "binary", isText: false };
    } finally {
      await fd.close();
    }
  } catch {
    // If we can't read the file, fall back to extension.
    return { type: byExt ?? "binary", isText: byExt === "text" };
  }
}

/**
 * @param {number} bytes
 */
export function formatFileSize(bytes) {
  if (bytes === 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

/**
 * Return a UTF-8-safe prefix whose encoded size stays within `maxBytes`.
 * Iterating code points avoids splitting a multibyte character at the limit.
 * @param {string} content
 * @param {number} maxBytes
 * @returns {{ content: string, truncated: boolean }}
 */
function truncateUtf8(content, maxBytes) {
  if (Buffer.byteLength(content, "utf8") <= maxBytes) return { content, truncated: false };

  const parts = [];
  let bytes = 0;
  for (const character of content) {
    const characterBytes = Buffer.byteLength(character, "utf8");
    if (bytes + characterBytes > maxBytes) break;
    parts.push(character);
    bytes += characterBytes;
  }
  return { content: parts.join(""), truncated: true };
}

export class FileService {
  /**
   * @param {string} root
   */
  constructor(root) {
    this.root = root;
  }

  /**
   * Atomic write: write to a temp file then rename. Prevents partial writes
   * from corrupting the target on crash.
   * @param {string} filePath
   * @param {string | Buffer} content
   */
  async _atomicWrite(filePath, content) {
    const tmp = `${filePath}.tmp-${process.pid}-${Math.random().toString(36).slice(2, 8)}`;
    if (Buffer.isBuffer(content)) {
      await fs.writeFile(tmp, content);
    } else {
      await fs.writeFile(tmp, content, "utf8");
    }
    await fs.rename(tmp, filePath);
  }

  /**
   * Update the root directory in-process (MCP Roots / set_root bridge).
   * The new root must exist and be a directory; containment is re-established
   * against it for all subsequent operations.
   * @param {string} newRoot
   */
  async setRoot(newRoot) {
    this.root = await validateRoot(newRoot);
    return this.root;
  }

  /**
   * Wraps fs errors with contextual hints about the plugin root.
   * @param {Promise<T>} p
   * @returns {Promise<T>}
   * @template T
   */
  async _wrap(p) {
    try {
      return await p;
    } catch (error) {
      if (error && typeof error === "object" && error.code === "ENOENT") {
        const hint = `Path not found. Files plugin root is "${this.root}". Use paths relative to that root (e.g. "" for root, "Documents" for a subdirectory).`;
        throw new Error(`${error.message}. ${hint}`);
      }
      throw error;
    }
  }

  /**
   * @param {string} input
   */
  async listDir(input) {
    const dir = resolvePath(this.root, input);
    const entries = await this._wrap(fs.readdir(dir, { withFileTypes: true }));
    const items = await Promise.all(
      entries.map(async (entry) => {
        const entryPath = path.join(dir, entry.name);
        const stat = await fs.stat(entryPath).catch(() => null);
        if (!stat) return null;
        return {
          name: entry.name,
          path: relativePosix(this.root, entryPath, entry.name),
          isDir: stat.isDirectory(),
          isFile: stat.isFile(),
          isSymlink: entry.isSymbolicLink(),
          size: stat.isFile() ? stat.size : 0,
          modified: stat.mtime.toISOString(),
          created: stat.birthtime.toISOString(),
          type: stat.isDirectory() ? "dir" : detectFileType(entry.name),
        };
      }),
    );
    return items.filter(Boolean).sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
  }

  /**
   * @param {string} input
   * @param {number} depth
   */
  async tree(input, depth = 3, opts = {}) {
    const { exclude = [], includeFiles = true } = opts;
    const clampedDepth = Math.min(Math.max(depth, 1), MAX_TREE_DEPTH);
    const dir = resolvePath(this.root, input);
    await this._wrap(fs.stat(dir));
    return this._buildTree(dir, clampedDepth, { exclude, includeFiles });
  }

  async _buildTree(dir, depth, opts) {
    const { exclude, includeFiles } = opts;
    if (depth <= 0) return null;
    const entries = await fs.readdir(dir, { withFileTypes: true }).catch(() => []);
    const children = await Promise.all(
      entries.map(async (entry) => {
        if (shouldExclude(entry.name, exclude)) return null;
        const entryPath = path.join(dir, entry.name);
        const stat = await fs.stat(entryPath).catch(() => null);
        if (!stat) return null;
        const isDir = stat.isDirectory();
        if (!includeFiles && !isDir) return null;
        const node = {
          name: entry.name,
          path: relativePosix(this.root, entryPath, entry.name),
          isDir,
          size: stat.isFile() ? stat.size : 0,
          modified: stat.mtime.toISOString(),
          type: isDir ? "dir" : detectFileType(entry.name),
        };
        if (isDir && depth > 1) {
          node.children = await this._buildTree(entryPath, depth - 1, opts);
        }
        return node;
      }),
    );
    return children.filter(Boolean).sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
  }

  /**
   * @param {string} input
   * @param {object} opts
   * @param {number} [opts.head]
   * @param {number} [opts.tail]
   * @param {number} [opts.start] 1-based start line
   * @param {number} [opts.end] 1-based end line (inclusive)
   * @param {boolean} [opts.lineNumbers] prefix each line with `NNN|`
   * @param {number} [opts.maxBytes] maximum UTF-8 bytes returned in content
   */
  async readFile(input, opts = {}) {
    const { head, tail, start, end, lineNumbers = false, maxBytes = MAX_READ_BYTES } = opts;
    const filePath = resolvePath(this.root, input);
    const stat = await this._wrap(fs.stat(filePath));
    // Authoritative binary detection: sniff magic bytes + NUL/control char ratio.
    // This catches text files without standard extensions (go.mod, Makefile, etc.)
    // and rejects actual binaries regardless of extension.
    const detected = await detectFileTypeByContent(filePath);
    if (!detected.isText) {
      throw new Error(`File is binary (type=${detected.type}); read only supports text. Use info to inspect.`);
    }
    const content = await fs.readFile(filePath, "utf8");
    const lines = splitLines(content);

    let selected;
    let truncatedReason = null;

    if (start && end) {
      const s = Math.max(1, start);
      const e = Math.min(lines.length, end);
      selected = lines.slice(s - 1, e);
      truncatedReason = "startEnd";
    } else if (head && head > 0) {
      selected = lines.slice(0, head);
      truncatedReason = "head";
    } else if (tail && tail > 0) {
      selected = lines.slice(-tail);
      truncatedReason = "tail";
    } else {
      selected = lines;
    }

    const rangeTruncated = truncatedReason !== null;
    const selectedOutput = lineNumbers
      ? selected.map((line, i) => {
          const lineNo = truncatedReason === "startEnd" ? start + i : (truncatedReason === "tail" ? lines.length - selected.length + i + 1 : i + 1);
          return `${String(lineNo).padStart(6, " ")}|${line}`;
        }).join("\n")
      : selected.join("\n");
    const { content: output, truncated: byteTruncated } = truncateUtf8(selectedOutput, maxBytes);
    const reason = byteTruncated ? "maxBytes" : truncatedReason;

    return {
      content: output,
      totalLines: lines.length,
      totalBytes: stat.size,
      truncated: rangeTruncated || byteTruncated,
      ...(reason ? { truncatedReason: reason } : {}),
    };
  }

  /**
   * @param {string} input
   * @param {string} content
   * @param {{ encoding?: "utf8" | "base64" }} [options] When encoding is
   *   "base64", content is decoded to a Buffer before writing so binary
   *   uploads survive byte-for-byte (used by the Files UI upload/drop path).
   */
  async writeFile(input, content, options = {}) {
    const filePath = resolvePath(this.root, input);
    await fs.mkdir(path.dirname(filePath), { recursive: true });
    const payload = options.encoding === "base64" ? Buffer.from(content, "base64") : content;
    await this._atomicWrite(filePath, payload);
    return { path: relativePosix(this.root, filePath, path.basename(filePath)), written: true };
  }

  /**
   * Create an empty directory, including any missing parents.
   * @param {string} input
   */
  async makeDir(input) {
    const dirPath = resolvePath(this.root, input);
    await fs.mkdir(dirPath, { recursive: true });
    return { path: relativePosix(this.root, dirPath, path.basename(dirPath)), created: true };
  }

  /**
   * @param {string} source
   * @param {string} destination
   */
  async moveFile(source, destination) {
    const src = resolvePath(this.root, source);
    const dst = resolvePath(this.root, destination);
    await fs.mkdir(path.dirname(dst), { recursive: true });
    await fs.rename(src, dst);
    return { from: relativePosix(this.root, src), to: relativePosix(this.root, dst), moved: true };
  }

  /**
   * @param {string} input
   * @param {boolean} recursive
   */
  async deleteFile(input, recursive) {
    const target = resolvePath(this.root, input);
    const stat = await this._wrap(fs.stat(target));
    if (stat.isDirectory() && !recursive) {
      const entries = await fs.readdir(target);
      if (entries.length > 0) {
        throw new Error("Directory is not empty. Use recursive=true to delete.");
      }
    }
    await fs.rm(target, { recursive });
    return { path: relativePosix(this.root, target, path.basename(target)), deleted: true };
  }

  /**
   * @param {string} input
   * @param {string} pattern
   */
  async searchFiles(input, pattern, opts = {}) {
    const { exclude = [], type = "any", maxDepth = MAX_TREE_DEPTH } = opts;
    const dir = resolvePath(this.root, input);
    await this._wrap(fs.stat(dir));
    const regex = globToRegex(pattern);
    const results = [];
    await this._searchRecursive(dir, regex, results, { exclude, type, maxDepth, currentDepth: 1 });
    const truncated = results.length > MAX_SEARCH_RESULTS;
    return {
      results: results.slice(0, MAX_SEARCH_RESULTS),
      meta: { truncated, count: Math.min(results.length, MAX_SEARCH_RESULTS), cap: MAX_SEARCH_RESULTS },
    };
  }

  async _searchRecursive(dir, regex, results, opts) {
    const { exclude, type, maxDepth, currentDepth } = opts;
    if (results.length >= MAX_SEARCH_RESULTS || currentDepth > maxDepth) return;
    const entries = await fs.readdir(dir, { withFileTypes: true }).catch(() => []);
    for (const entry of entries) {
      if (results.length >= MAX_SEARCH_RESULTS) return;
      if (shouldExclude(entry.name, exclude)) continue;
      const entryPath = path.join(dir, entry.name);
      const stat = await fs.stat(entryPath).catch(() => null);
      if (!stat) continue;
      const isDir = stat.isDirectory();
      if (regex.test(entry.name)) {
        if (type === "file" && isDir) continue;
        if (type === "dir" && !isDir) continue;
        results.push({
          name: entry.name,
          path: relativePosix(this.root, entryPath, entry.name),
          isDir,
          size: stat.isFile() ? stat.size : 0,
          modified: stat.mtime.toISOString(),
          type: isDir ? "dir" : detectFileType(entry.name),
        });
      }
      if (isDir) {
        await this._searchRecursive(entryPath, regex, results, { ...opts, currentDepth: currentDepth + 1 });
      }
    }
  }

  /**
   * Search file contents for a regex pattern (like grep).
   * @param {string} input - directory or single file to search
   * @param {string} pattern - regex pattern
   * @param {object} opts
   * @param {string} [opts.glob] - optional file name glob filter (directory mode only)
   * @param {number} [opts.before] - context lines before match
   * @param {number} [opts.after] - context lines after match
   * @param {boolean} [opts.ignoreCase] - case-insensitive matching
   * @param {string[]} [opts.exclude] - glob patterns to exclude entries
   * @param {number} [opts.maxResults] - max results (default 500)
   */
  async grepFiles(input, pattern, opts = {}) {
    const { glob, before = 0, after = 0, ignoreCase = false, exclude = [], maxResults = MAX_SEARCH_RESULTS } = opts;
    const target = resolvePath(this.root, input);
    const stat = await this._wrap(fs.stat(target));
    const regex = new RegExp(pattern, ignoreCase ? "i" : "");
    const globRegex = glob ? globToRegex(glob) : null;
    const cap = Math.min(maxResults, MAX_SEARCH_RESULTS);
    const results = [];
    if (stat.isFile()) {
      // Agents often pass a file path (like rg/path grep). Previously this fell
      // through to readdir + empty catch → silent empty results while read still
      // worked on the same path.
      const name = path.basename(target);
      if (!globRegex || globRegex.test(name)) {
        await this._grepOneFile(target, name, regex, results, { before, after, cap });
      }
    } else if (stat.isDirectory()) {
      await this._grepRecursive(target, regex, globRegex, results, { before, after, exclude, cap });
    } else {
      throw new Error(`Path is neither a file nor a directory: ${input || "."}`);
    }
    const truncated = results.length > cap;
    return {
      results: results.slice(0, cap),
      meta: { truncated, count: Math.min(results.length, cap), cap },
    };
  }

  async _grepOneFile(entryPath, entryName, regex, results, opts) {
    const { before, after, cap } = opts;
    if (results.length >= cap) return;
    // Skip files that are definitely binary by extension (fast path).
    // For unknown extensions, fall through to content-based detection below.
    const extType = detectFileType(entryName);
    if (extType !== "text" && extType !== "binary") return;
    const stat = await fs.stat(entryPath).catch(() => null);
    if (!stat || !stat.isFile() || stat.size > MAX_READ_BYTES) return;
    // Content-based detection: catches text files without standard extensions
    // (go.mod, Makefile) and rejects binaries with text extensions.
    const detected = await detectFileTypeByContent(entryPath);
    if (!detected.isText) return;
    const content = await fs.readFile(entryPath, "utf8").catch(() => null);
    if (!content) return;
    const lines = splitLines(content);
    for (let i = 0; i < lines.length; i++) {
      if (results.length >= cap) break;
      if (regex.test(lines[i])) {
        const rawLine = lines[i];
        const lineContent = rawLine.length > MAX_GREP_LINE_LENGTH
          ? rawLine.slice(0, MAX_GREP_LINE_LENGTH) + "…(truncated)"
          : rawLine;
        results.push({
          path: relativePosix(this.root, entryPath, entryName),
          line: i + 1,
          content: lineContent,
          ...(before > 0 ? { before: lines.slice(Math.max(0, i - before), i) } : {}),
          ...(after > 0 ? { after: lines.slice(i + 1, i + 1 + after) } : {}),
        });
      }
    }
  }

  async _grepRecursive(dir, regex, globRegex, results, opts) {
    const { before, after, exclude, cap } = opts;
    if (results.length >= cap) return;
    const entries = await fs.readdir(dir, { withFileTypes: true }).catch(() => []);
    for (const entry of entries) {
      if (results.length >= cap) return;
      if (shouldExclude(entry.name, exclude)) continue;
      const entryPath = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        await this._grepRecursive(entryPath, regex, globRegex, results, opts);
        continue;
      }
      if (globRegex && !globRegex.test(entry.name)) continue;
      await this._grepOneFile(entryPath, entry.name, regex, results, { before, after, cap });
    }
  }

  /**
   * Apply one or more string replacements to a file.
   * @param {string} input
   * @param {Array<{old_string: string, new_string: string, replace_all?: boolean}>} edits
   * @param {boolean} preview — if true, return a diff without writing
   */
  async patchFile(input, edits, preview = false) {
    const filePath = resolvePath(this.root, input);
    const stat = await this._wrap(fs.stat(filePath));
    if (stat.size > MAX_READ_BYTES) {
      throw new Error(`File too large (${formatFileSize(stat.size)}), max ${formatFileSize(MAX_READ_BYTES)}`);
    }
    // Reject binary files — patching them as UTF-8 would corrupt the content.
    const detected = await detectFileTypeByContent(filePath);
    if (!detected.isText) {
      throw new Error(`File is binary (type=${detected.type}); patch only supports text files.`);
    }
    let content = await fs.readFile(filePath, "utf8");
    const occurrences = [];
    for (const edit of edits) {
      const { old_string, new_string, replace_all = false } = edit;
      if (!content.includes(old_string)) {
        throw new Error(`old_string not found in file. Ensure the string matches exactly, including whitespace and indentation. (edit ${occurrences.length + 1} of ${edits.length})`);
      }
      let count;
      if (replace_all) {
        const parts = content.split(old_string);
        count = parts.length - 1;
        content = parts.join(new_string);
      } else {
        count = 1;
        content = content.replace(old_string, new_string);
      }
      occurrences.push(count);
    }
    if (preview) {
      return { path: relativePosix(this.root, filePath, path.basename(filePath)), patched: false, applied: 0, occurrences, preview: content };
    }
    await this._atomicWrite(filePath, content);
    return { path: relativePosix(this.root, filePath, path.basename(filePath)), patched: true, applied: edits.length, occurrences };
  }

  /**
   * Copy a file or directory recursively.
   * @param {string} input - source path
   * @param {string} destination - destination path
   */
  async copyFile(input, destination) {
    const src = resolvePath(this.root, input);
    const dst = resolvePath(this.root, destination);
    await this._wrap(fs.stat(src));
    await fs.cp(src, dst, { recursive: true });
    return { from: relativePosix(this.root, src, path.basename(src)), to: relativePosix(this.root, dst, path.basename(dst)), copied: true };
  }

  /**
   * Append content to the end of a file (creates it if it doesn't exist).
   * @param {string} input
   * @param {string} content
   */
  async appendFile(input, content) {
    const filePath = resolvePath(this.root, input);
    await fs.mkdir(path.dirname(filePath), { recursive: true });
    const existing = await fs.readFile(filePath, "utf8").catch(() => "");
    await this._atomicWrite(filePath, existing + content);
    return { path: relativePosix(this.root, filePath, path.basename(filePath)), appended: true };
  }

  /**
   * @param {string} input
   */
  async fileInfo(input) {
    const filePath = resolvePath(this.root, input);
    const stat = await this._wrap(fs.stat(filePath));
    // Use content-based detection for files (magic bytes + NUL ratio).
    // This is more accurate than extension-only for files like go.mod, Makefile, etc.
    let fileType = stat.isDirectory() ? "dir" : detectFileType(filePath);
    if (stat.isFile()) {
      const detected = await detectFileTypeByContent(filePath);
      fileType = detected.type;
    }
    return {
      name: path.basename(filePath),
      path: relativePosix(this.root, filePath, path.basename(filePath)),
      isDir: stat.isDirectory(),
      isFile: stat.isFile(),
      isSymlink: stat.isSymbolicLink(),
      size: stat.size,
      modified: stat.mtime.toISOString(),
      created: stat.birthtime.toISOString(),
      type: fileType,
      permissions: stat.mode.toString(8),
    };
  }

  /**
   * Check if a path exists. Does NOT throw on ENOENT (that's the contract).
   * @param {string} input
   */
  async existsFile(input) {
    const filePath = resolvePath(this.root, input);
    const stat = await fs.stat(filePath).catch(() => null);
    if (!stat) return { path: input, exists: false, isFile: false, isDir: false };
    return {
      path: relativePosix(this.root, filePath, path.basename(filePath)),
      exists: true,
      isFile: stat.isFile(),
      isDir: stat.isDirectory(),
    };
  }

  /**
   * Create an empty file if it doesn't exist, or update atime+mtime if it does.
   * @param {string} input
   * @param {object} opts
   * @param {boolean} [opts.createParents] - create parent dirs (default true)
   * @param {boolean} [opts.updateOnly] - throw ENOENT if file doesn't exist
   */
  async touchFile(input, opts = {}) {
    const { createParents = true, updateOnly = false } = opts;
    const filePath = resolvePath(this.root, input);
    const existing = await fs.stat(filePath).catch(() => null);
    if (!existing) {
      if (updateOnly) {
        throw new Error(`File does not exist: ${input}. Use updateOnly=false to create it.`);
      }
      if (createParents) {
        await fs.mkdir(path.dirname(filePath), { recursive: true });
      }
      await this._atomicWrite(filePath, "");
      return { path: relativePosix(this.root, filePath, path.basename(filePath)), created: true, touched: false };
    }
    const now = new Date();
    await fs.utimes(filePath, now, now);
    return { path: relativePosix(this.root, filePath, path.basename(filePath)), created: false, touched: true };
  }
}

/**
 * @param {string} pattern
 */
function globToRegex(pattern) {
  const escaped = pattern
    .replace(/[.+^${}()|[\]\\]/g, "\\$&")
    .replace(/\*/g, ".*")
    .replace(/\?/g, ".");
  return new RegExp(`^${escaped}$`, "i");
}

/**
 * Check if a name matches any exclude glob pattern.
 * @param {string} name
 * @param {readonly string[]} excludeGlobs
 */
function shouldExclude(name, excludeGlobs) {
  if (!excludeGlobs || excludeGlobs.length === 0) return false;
  return excludeGlobs.some((glob) => globToRegex(glob).test(name));
}
