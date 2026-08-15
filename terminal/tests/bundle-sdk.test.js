import { describe, it, expect } from "vitest";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const bundlePath = path.resolve(__dirname, "../mcp/server.cjs");

/**
 * Regression guard for Terminal MCP packaging.
 *
 * The Terminal plugin's server.cjs must be an esbuild bundle with the MCP SDK
 * inlined — not a hand-written CJS file that require()s @modelcontextprotocol/sdk
 * at runtime. In a packaged app, the plugins directory has no node_modules, so
 * a bare require for the SDK crashes with MODULE_NOT_FOUND. node-pty is
 * externalized (native module) and staged separately by the desktop packaging
 * step.
 */
describe("server.cjs bundle packaging", () => {
  it("bundle exists", () => {
    expect(fs.existsSync(bundlePath)).toBe(true);
  });

  it("does not bare-require @modelcontextprotocol/sdk (SDK is inlined)", () => {
    const source = fs.readFileSync(bundlePath, "utf8");
    expect(source).not.toContain('require("@modelcontextprotocol/sdk');
  });

  it("still requires node-pty as an external native module", () => {
    const source = fs.readFileSync(bundlePath, "utf8");
    expect(source).toContain("node-pty");
  });
});
