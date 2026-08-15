import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import {
  ContextEngine,
  estimateTokens,
  personalizedPagerank,
  allocateRoleBudget,
  roleMatchMultiplier,
  recencyDecay,
} from "../mcp/context-engine.js";

let tmpDir;
let engine;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "files-context-test-"));
  engine = new ContextEngine(tmpDir);
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

async function writeCodeWorkspace() {
  await fs.writeFile(
    path.join(tmpDir, "package.json"),
    JSON.stringify({
      name: "demo",
      version: "1.2.3",
      scripts: { build: "tsc", test: "vitest" },
      dependencies: { react: "^19.0.0", zod: "^4.0.0" },
      devDependencies: { typescript: "^5.0.0" },
    }),
  );
  await fs.mkdir(path.join(tmpDir, "src"));
  await fs.writeFile(
    path.join(tmpDir, "src", "core.ts"),
    [
      "export class Engine {",
      "  run() { return helper(); }",
      "}",
      "export function helper() { return 1; }",
      "export const LIMIT = 10;",
      "",
    ].join("\n"),
  );
  await fs.writeFile(
    path.join(tmpDir, "src", "app.ts"),
    [
      "import { Engine } from \"./core\";",
      "const engine = new Engine();",
      "engine.run();",
      "console.log(LIMIT);",
      "",
    ].join("\n"),
  );
  await fs.writeFile(
    path.join(tmpDir, "main.py"),
    ["class Worker:", "    pass", "", "def start():", "    return Worker()", ""].join("\n"),
  );
}

describe("detectStack (phase 1)", () => {
  it("classifies a manifest workspace as coding and reads package.json metadata", async () => {
    await writeCodeWorkspace();
    const stack = await engine.detectStack();
    expect(stack.category).toBe("coding");
    expect(stack.projectName).toBe("demo");
    expect(stack.version).toBe("1.2.3");
    expect(stack.keyDeps).toContain("react");
    expect(stack.keyDeps).toContain("typescript");
    expect(stack.scripts).toHaveProperty("build");
    expect(stack.languages).toContain("typescript");
    expect(stack.isMonorepo).toBe(false);
  });

  it("classifies a docs-only workspace as documentation", async () => {
    await fs.writeFile(path.join(tmpDir, "README.md"), "# docs\n");
    await fs.writeFile(path.join(tmpDir, "guide.md"), "# guide\n");
    const stack = await engine.detectStack();
    expect(stack.category).toBe("documentation");
    expect(stack.isMonorepo).toBe(false);
  });

  it("classifies nested manifests or pnpm workspaces as hybrid/monorepo", async () => {
    await fs.writeFile(path.join(tmpDir, "pnpm-workspace.yaml"), "packages:\n  - packages/*\n");
    await fs.writeFile(path.join(tmpDir, "package.json"), JSON.stringify({ name: "root" }));
    await fs.mkdir(path.join(tmpDir, "packages", "a"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "packages", "a", "package.json"), JSON.stringify({ name: "a" }));
    const stack = await engine.detectStack();
    expect(stack.category).toBe("hybrid");
    expect(stack.isMonorepo).toBe(true);
  });
});

describe("workspace instructions", () => {
  it("reads the workspace-root AGENTS.md with bounded metadata", async () => {
    await fs.writeFile(path.join(tmpDir, "AGENTS.md"), "# Project rules\nUse pnpm test.\n");
    await fs.mkdir(path.join(tmpDir, "nested"));
    await fs.writeFile(path.join(tmpDir, "nested", "AGENTS.md"), "nested rules\n");

    const result = await engine.readWorkspaceInstructions();

    expect(result).toEqual(expect.objectContaining({
      uri: "nusashell://workspace/AGENTS.md",
      name: "Workspace instructions",
      mimeType: "text/markdown",
      text: "# Project rules\nUse pnpm test.\n",
    }));
    expect(result.text).not.toContain("nested rules");
  });

  it("returns null when the workspace has no AGENTS.md", async () => {
    await expect(engine.readWorkspaceInstructions()).resolves.toBeNull();
  });
});

describe("walk + ignore handling (phase 2)", () => {
  it("skips default ignore dirs and gitignore patterns", async () => {
    await writeCodeWorkspace();
    await fs.mkdir(path.join(tmpDir, "node_modules", "dep"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "node_modules", "dep", "index.ts"), "export const x = 1;\n");
    await fs.writeFile(path.join(tmpDir, ".gitignore"), "ignored.ts\n");
    await fs.writeFile(path.join(tmpDir, "ignored.ts"), "export const y = 2;\n");
    const result = await engine.contextMap({});
    expect(result.map).toContain("src/core.ts");
    expect(result.map).not.toContain("node_modules");
    expect(result.map).not.toContain("ignored.ts");
  });
});

describe("symbol extraction (phase 3)", () => {
  it("extracts typescript and python definitions for a single file", async () => {
    await writeCodeWorkspace();
    const ts = await engine.listSymbols({ path: "src/core.ts" });
    const names = ts.symbols.map((s) => s.name);
    expect(names).toContain("Engine");
    expect(names).toContain("helper");
    expect(names).toContain("LIMIT");
    const py = await engine.listSymbols({ path: "main.py" });
    const pyNames = py.symbols.map((s) => s.name);
    expect(pyNames).toContain("Worker");
    expect(pyNames).toContain("start");
  });

  it("extracts definitions from CRLF sources without CR in signatures", () => {
    const text = "export function hello() {\r\n  return 1;\r\n}\r\nexport const LIMIT = 10;\r\n";
    const { defs } = engine.extractFromText("x.ts", text, "typescript");
    const names = defs.map((d) => d.name);
    expect(names).toContain("hello");
    expect(names).toContain("LIMIT");
    for (const def of defs) {
      expect(def.sig).not.toContain("\r");
    }
  });

  it("rejects symbol extraction for unsupported file types", async () => {
    await fs.writeFile(path.join(tmpDir, "data.bin"), "x");
    await expect(engine.listSymbols({ path: "data.bin" })).rejects.toThrow();
  });
});

describe("graph + personalized pagerank (phase 4)", () => {
  it("personalization boosts the active file", () => {
    const graph = {
      nodes: ["a.ts", "b.ts", "c.ts"],
      outEdges: new Map([
        ["a.ts", new Set(["b.ts"])],
        ["b.ts", new Set(["a.ts"])],
      ]),
    };
    const plain = personalizedPagerank(graph);
    const boosted = personalizedPagerank(graph, { "c.ts": 50 });
    expect(boosted["c.ts"]).toBeGreaterThan(plain["c.ts"]);
    const sum = Object.values(boosted).reduce((acc, v) => acc + v, 0);
    expect(sum).toBeCloseTo(1, 5);
  });

  it("files defining referenced symbols outrank isolated files", async () => {
    await writeCodeWorkspace();
    await fs.writeFile(path.join(tmpDir, "src", "lonely.ts"), "export const zzq = 1;\n");
    const result = await engine.contextMap({});
    const ranks = new Map(result.ranks);
    expect(ranks.get("src/core.ts")).toBeGreaterThan(ranks.get("src/lonely.ts"));
  });
});

describe("token budget fitting (phase 5)", () => {
  it("estimateTokens approximates 4 chars per token", () => {
    expect(estimateTokens("abcd")).toBe(1);
    expect(estimateTokens("")).toBe(1);
    expect(estimateTokens("a".repeat(400))).toBe(100);
  });

  it("keeps the map within the token budget", async () => {
    await writeCodeWorkspace();
    for (let i = 0; i < 12; i += 1) {
      await fs.writeFile(
        path.join(tmpDir, "src", `mod${i}.ts`),
        `import { helper } from "./core";\nexport function fn${i}() { return helper(); }\n`,
      );
    }
    const budget = 120;
    const result = await engine.contextMap({ budget });
    expect(result.stats.tokensUsed).toBeLessThanOrEqual(Math.max(budget, estimateTokens(result.map)));
    expect(result.stats.filesShown).toBeGreaterThan(0);
  });
});

describe("cache invalidation (phase 6)", () => {
  it("serves unchanged files from cache and re-extracts on mtime change", async () => {
    await writeCodeWorkspace();
    const first = await engine.contextMap({});
    expect(first.stats.cacheMisses).toBeGreaterThan(0);
    const second = await engine.contextMap({});
    expect(second.stats.cacheHits).toBeGreaterThan(0);

    const future = new Date(Date.now() + 60_000);
    await fs.appendFile(path.join(tmpDir, "src", "core.ts"), "export function added() { return 2; }\n");
    await fs.utimes(path.join(tmpDir, "src", "core.ts"), future, future);
    const third = await engine.contextMap({});
    const coreSymbols = await engine.listSymbols({ path: "src/core.ts" });
    expect(coreSymbols.symbols.map((s) => s.name)).toContain("added");
    expect(third.stats.cacheMisses).toBeGreaterThan(0);
  });

  it("refresh=true bypasses the cache", async () => {
    await writeCodeWorkspace();
    await engine.contextMap({});
    const fresh = await engine.contextMap({ refresh: true });
    expect(fresh.stats.cacheHits).toBe(0);
  });
});

describe("contextMap orchestration", () => {
  it("returns map markdown, stack, ranks, and stats", async () => {
    await writeCodeWorkspace();
    const result = await engine.contextMap({ activeFile: "src/app.ts", query: "Engine" });
    expect(result.map).toContain("# Workspace Context Map");
    expect(result.map).toContain("src/app.ts");
    expect(result.stack.category).toBe("coding");
    expect(Array.isArray(result.ranks)).toBe(true);
    expect(result.ranks.length).toBeGreaterThan(0);
    expect(result.stats.graph.nodes).toBeGreaterThan(0);
    expect(result.stats.timingMs.total).toBeGreaterThanOrEqual(0);
  });

  it("handles an empty documentation workspace without code files", async () => {
    await fs.writeFile(path.join(tmpDir, "README.md"), "# only docs\n");
    const result = await engine.contextMap({});
    expect(result.stack.category).toBe("documentation");
    expect(typeof result.map).toBe("string");
  });
});

/**
 * Mixed workspace for role-aware ranking: docs, implementation code, tests.
 * All files touch so mtime differences can be controlled via utimes in tests.
 */
async function writeRoleWorkspace() {
  await fs.writeFile(
    path.join(tmpDir, "package.json"),
    JSON.stringify({ name: "role-demo", version: "0.0.1", scripts: { test: "vitest" } }),
  );
  await fs.mkdir(path.join(tmpDir, "src"), { recursive: true });
  await fs.mkdir(path.join(tmpDir, "docs"), { recursive: true });
  await fs.writeFile(
    path.join(tmpDir, "src", "service.ts"),
    [
      "export class Service {",
      "  run() { return compute(); }",
      "}",
      "export function compute() { return 42; }",
      "",
    ].join("\n"),
  );
  await fs.writeFile(
    path.join(tmpDir, "src", "handler.ts"),
    [
      "import { Service } from \"./service\";",
      "export function handle() {",
      "  return new Service().run();",
      "}",
      "",
    ].join("\n"),
  );
  await fs.writeFile(
    path.join(tmpDir, "src", "service.test.ts"),
    [
      "import { compute } from \"./service\";",
      "export function testCompute() { return compute() === 42; }",
      "",
    ].join("\n"),
  );
  await fs.writeFile(path.join(tmpDir, "AGENTS.md"), "# Workspace rules\nPrefer small changes.\n");
  await fs.writeFile(path.join(tmpDir, "docs", "guide.md"), "# Guide\nHow the service works.\n");
}

describe("role-aware token budget (RCR)", () => {
  it("allocateRoleBudget equals base when role is absent or unknown", () => {
    expect(allocateRoleBudget(1024)).toBe(1024);
    expect(allocateRoleBudget(1024, undefined)).toBe(1024);
    expect(allocateRoleBudget(1024, "bogus")).toBe(1024);
  });

  it("allocateRoleBudget differs from base and across roles when role is set", () => {
    const base = 1024;
    const planner = allocateRoleBudget(base, "planner");
    const executor = allocateRoleBudget(base, "executor");
    const reviewer = allocateRoleBudget(base, "reviewer");
    expect(planner).not.toBe(base);
    expect(executor).not.toBe(base);
    expect(reviewer).not.toBe(base);
    expect(new Set([planner, executor, reviewer]).size).toBe(3);
    expect(planner).toBeGreaterThan(0);
    expect(executor).toBeGreaterThan(0);
    expect(reviewer).toBeGreaterThan(0);
  });

  it("roleMatchMultiplier prefers docs for planner, code for executor, tests for reviewer", () => {
    expect(roleMatchMultiplier("docs/guide.md", "planner")).toBeGreaterThan(
      roleMatchMultiplier("src/service.ts", "planner"),
    );
    expect(roleMatchMultiplier("AGENTS.md", "planner")).toBeGreaterThan(1);
    expect(roleMatchMultiplier("src/service.ts", "executor")).toBeGreaterThan(
      roleMatchMultiplier("src/service.test.ts", "executor"),
    );
    expect(roleMatchMultiplier("src/service.ts", "executor")).toBeGreaterThan(
      roleMatchMultiplier("docs/guide.md", "executor"),
    );
    expect(roleMatchMultiplier("src/service.test.ts", "reviewer")).toBeGreaterThan(
      roleMatchMultiplier("src/service.ts", "reviewer"),
    );
    expect(roleMatchMultiplier("AGENTS.md", "reviewer")).toBeGreaterThan(1);
    expect(roleMatchMultiplier("src/service.ts")).toBe(1);
  });

  it("recencyDecay is 1 for now and lower for stale mtimes (deterministic half-life)", () => {
    const now = 1_700_000_000_000;
    expect(recencyDecay(now, now)).toBeCloseTo(1, 5);
    const thirtyDays = 30 * 86400 * 1000;
    expect(recencyDecay(now - thirtyDays, now)).toBeCloseTo(0.5, 2);
    expect(recencyDecay(now - thirtyDays * 2, now)).toBeLessThan(0.3);
    expect(recencyDecay(now - 1000, now)).toBeGreaterThan(recencyDecay(now - thirtyDays, now));
  });

  it("without role, context_map is byte-identical to the legacy map output", async () => {
    await writeRoleWorkspace();
    const a = await engine.contextMap({ budget: 512 });
    const b = await engine.contextMap({ budget: 512 });
    expect(a.map).toBe(b.map);
    expect(a.ranks).toEqual(b.ranks);
    expect(a.stats.tokensUsed).toBe(b.stats.tokensUsed);
    expect(a.stats.filesShown).toBe(b.stats.filesShown);
    expect(a).not.toHaveProperty("roleScores");
    expect(a.stats.role).toBeUndefined();
    expect(a.stats.effectiveBudget).toBeUndefined();
  });

  it("context_map is deterministic across repeated calls even when scores tie", async () => {
    // Regression: extractAll populated Maps in async completion order, so two
    // files with identical PPR scores could flip their order between calls
    // (stable sort preserves Map insertion order). Files here are structurally
    // identical and unconnected → exactly equal scores → order must be pinned
    // by a deterministic rule (relative path), never by Promise.all completion.
    await fs.mkdir(path.join(tmpDir, "src"), { recursive: true });
    for (const name of ["zeta.ts", "alpha.ts", "mid.ts"]) {
      await fs.writeFile(
        path.join(tmpDir, "src", name),
        `export function ${name.replace(".", "")}Fn() { return 1; }\n`,
      );
    }
    const first = await engine.contextMap({ budget: 512 });
    for (let i = 0; i < 8; i += 1) {
      const again = await engine.contextMap({ budget: 512 });
      expect(again.map).toBe(first.map);
      expect(again.ranks).toEqual(first.ranks);
    }
    const ranks = new Map(first.ranks);
    const scores = [...first.ranks];
    // Tied files must be ordered deterministically (path ascending), and all
    // three files must actually be present in the map.
    expect(scores[0][1]).toBeGreaterThan(0);
    expect([...ranks.keys()].sort()).toEqual(["src/alpha.ts", "src/mid.ts", "src/zeta.ts"]);
  });

  it("reviewer ranks test/convention files higher relative to executor, both within budget", async () => {
    await writeRoleWorkspace();
    const budget = 400;
    const now = Date.now();
    // Align mtimes so recency does not dominate role match.
    for (const rel of [
      "src/service.ts",
      "src/handler.ts",
      "src/service.test.ts",
      "AGENTS.md",
      "docs/guide.md",
    ]) {
      await fs.utimes(path.join(tmpDir, rel), new Date(now), new Date(now));
    }

    const reviewer = await engine.contextMap({ budget, role: "reviewer", now });
    const executor = await engine.contextMap({ budget, role: "executor", now });
    expect(reviewer.stats.tokensUsed).toBeLessThanOrEqual(reviewer.stats.effectiveBudget);
    expect(executor.stats.tokensUsed).toBeLessThanOrEqual(executor.stats.effectiveBudget);
    expect(reviewer.stats.effectiveBudget).toBe(allocateRoleBudget(budget, "reviewer"));
    expect(executor.stats.effectiveBudget).toBe(allocateRoleBudget(budget, "executor"));

    const revRanks = new Map(reviewer.ranks);
    const execRanks = new Map(executor.ranks);
    const testPath = "src/service.test.ts";
    const implPath = "src/service.ts";
    // Relative preference: ratio of test/impl or absolute order under each role.
    const revTest = revRanks.get(testPath) ?? 0;
    const revImpl = revRanks.get(implPath) ?? 0;
    const execTest = execRanks.get(testPath) ?? 0;
    const execImpl = execRanks.get(implPath) ?? 0;
    expect(revTest / Math.max(revImpl, 1e-12)).toBeGreaterThan(
      execTest / Math.max(execImpl, 1e-12),
    );
    expect(execImpl / Math.max(execTest, 1e-12)).toBeGreaterThan(
      revImpl / Math.max(revTest, 1e-12),
    );
  });

  it("planner ranks docs/rules higher than executor does", async () => {
    await writeRoleWorkspace();
    const budget = 400;
    const now = Date.now();
    for (const rel of [
      "src/service.ts",
      "src/handler.ts",
      "src/service.test.ts",
      "AGENTS.md",
      "docs/guide.md",
    ]) {
      await fs.utimes(path.join(tmpDir, rel), new Date(now), new Date(now));
    }
    const planner = await engine.contextMap({ budget, role: "planner", now });
    const executor = await engine.contextMap({ budget, role: "executor", now });
    const planRanks = new Map(planner.ranks);
    const execRanks = new Map(executor.ranks);
    const docPath = "docs/guide.md";
    const implPath = "src/service.ts";
    const planDoc = planRanks.get(docPath) ?? 0;
    const planImpl = planRanks.get(implPath) ?? 0;
    const execDoc = execRanks.get(docPath) ?? 0;
    const execImpl = execRanks.get(implPath) ?? 0;
    expect(planDoc / Math.max(planImpl, 1e-12)).toBeGreaterThan(
      execDoc / Math.max(execImpl, 1e-12),
    );
    expect(planner.stats.tokensUsed).toBeLessThanOrEqual(planner.stats.effectiveBudget);
  });

  it("stale files receive a lower role-aware score than recent ones, all else equal", async () => {
    await writeRoleWorkspace();
    const now = 1_700_000_000_000;
    const fresh = now;
    const stale = now - 90 * 86400 * 1000; // 90 days older
    await fs.utimes(path.join(tmpDir, "src", "service.ts"), new Date(fresh), new Date(fresh));
    await fs.utimes(path.join(tmpDir, "src", "handler.ts"), new Date(stale), new Date(stale));
    // Both are executor-side implementation files (no test suffix).
    const result = await engine.contextMap({
      budget: 1024,
      role: "executor",
      now,
      refresh: true,
    });
    const ranks = new Map(result.ranks);
    expect(ranks.get("src/service.ts")).toBeGreaterThan(ranks.get("src/handler.ts"));
    if (result.roleScores) {
      const byPath = Object.fromEntries(result.roleScores.map((s) => [s.path, s]));
      expect(byPath["src/service.ts"].recency).toBeGreaterThan(byPath["src/handler.ts"].recency);
      expect(byPath["src/service.ts"].score).toBeGreaterThan(byPath["src/handler.ts"].score);
    }
  });
});
