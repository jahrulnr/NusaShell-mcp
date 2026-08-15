// node-pty 1.1.0 ships spawn-helper without the execute bit on macOS
// prebuilds (microsoft/node-pty#850). This postinstall script fixes the
// permissions so pty.spawn() doesn't fail with "posix_spawnp failed".
// Upstream fix is in 1.2.0-beta but not yet on the latest dist-tag.
// Safe no-op on non-darwin platforms and when node-pty is absent.
const fs = require("fs");
const path = require("path");

if (process.platform === "darwin") {
  const prebuildsDir = path.join("node_modules", "node-pty", "prebuilds");
  try {
    for (const dir of fs.readdirSync(prebuildsDir)) {
      if (!dir.startsWith("darwin-")) continue;
      const helper = path.join(prebuildsDir, dir, "spawn-helper");
      try {
        fs.chmodSync(helper, 0o755);
      } catch {}
    }
  } catch {}
}
