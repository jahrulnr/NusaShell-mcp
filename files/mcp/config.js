import os from "node:os";
import path from "node:path";
import fs from "node:fs/promises";

/**
 * Resolves the root directory for file operations.
 *
 * Source precedence: NUSASHELL_FILES_ROOT → NUSASHELL_WORKSPACE → user home.
 * The workspace fallback binds the Files root to the conversation workspace
 * when the shell spawns the plugin with NUSASHELL_WORKSPACE (Phase 3 respawn
 * path). Roots (Phase 2) update the root in-process after spawn.
 *
 * The root must exist and be a directory.
 *
 * Path resolution is predictable, not jailing: `/` and absolute paths resolve
 * to OS-absolute paths; relative paths resolve against the root; `../`
 * traversal is allowed (escape is permitted). Security is the user/AI
 * provider's responsibility — see docs/architecture/security-boundary.md.
 *
 * @param {NodeJS.ProcessEnv | Record<string, string | undefined>} environment
 */
export async function loadRootFromEnvironment(environment = process.env) {
  const raw = environment.NUSASHELL_FILES_ROOT || environment.NUSASHELL_WORKSPACE;
  const root = raw ? path.resolve(raw) : os.homedir();
  return validateRoot(root);
}

/**
 * Validate that a path exists and is a directory, returning the resolved root.
 * @param {string} root
 */
export async function validateRoot(root) {
  const resolved = path.resolve(root);
  try {
    const stat = await fs.stat(resolved);
    if (!stat.isDirectory()) {
      throw new Error(`Files root is not a directory: ${resolved}`);
    }
  } catch (error) {
    if (error && error.code === "ENOENT") {
      throw new Error(`Files root does not exist: ${resolved}`);
    }
    throw error;
  }
  return resolved;
}

/**
 * Resolves a path relative to the root directory.
 *
 * `/` and absolute paths resolve to OS-absolute paths. Relative paths resolve
 * against the root; `../` traversal is allowed (escape is permitted). The root
 * is a convenience for relative path resolution, not a jail. Security is the
 * user/AI provider's responsibility — see docs/architecture/security-boundary.md.
 *
 * @param {string} root
 * @param {string} input
 */
export function resolvePath(root, input) {
  if (!input || input === "") return root;
  // Resolve absolute input without root so Windows does not inherit root's drive
  // letter (path.resolve(root, "/foo") can differ from path.resolve("/foo")).
  if (path.isAbsolute(input)) {
    return path.resolve(input);
  }
  return path.resolve(root, input);
}

/**
 * Normalize separators to `/` so agent-facing relative paths are stable on
 * Windows (`\`) and Unix.
 * @param {string} value
 * @returns {string}
 */
export function toPosixPath(value) {
  if (!value) return value;
  return String(value).replace(/\\/g, "/");
}

/**
 * Workspace-relative path with POSIX separators.
 * @param {string} root
 * @param {string} absolutePath
 * @param {string} [fallback=""] used when `path.relative` is empty
 * @returns {string}
 */
export function relativePosix(root, absolutePath, fallback = "") {
  const rel = path.relative(root, absolutePath);
  return toPosixPath(rel || fallback);
}

/**
 * Split text into lines accepting both LF and CRLF without leaving trailing CR.
 * @param {string} text
 * @returns {string[]}
 */
export function splitLines(text) {
  return text.split(/\r?\n/);
}
