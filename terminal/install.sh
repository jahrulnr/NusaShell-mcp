#!/usr/bin/env bash
# Terminal plugin no longer needs a plugin-local npm install — the MCP SDK is
# bundled into mcp/server.cjs via esbuild, and node-pty is staged by the
# desktop packaging step. Run `pnpm build` from the plugin dir to rebuild
# the bundle after changing mcp/server.js.
cd "$(dirname "$0")" && pnpm build 2>&1
