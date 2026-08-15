import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import {
  tokenize,
  chunkLines,
  BM25,
  tfidfDenseScores,
  reciprocalRankFusion,
  crossEncoderScore,
  runRetrieval,
  RetrievalEngine,
} from "../mcp/search-relevant.js";

describe("tokenize", () => {
  it("lowercases and splits on non-identifier boundaries", () => {
    expect(tokenize("Plugin Runtime Lifecycle")).toEqual(["plugin", "runtime", "lifecycle"]);
    expect(tokenize("camelCaseName snake_case_name")).toEqual([
      "camelcasename", "snake", "case", "name",
    ]);
  });

  it("drops single-character tokens and keeps underscores", () => {
    expect(tokenize("a b _x yy")).toEqual(["_x", "yy"]);
    expect(tokenize("")).toEqual([]);
  });
});

describe("chunkLines", () => {
  it("produces one chunk for a short file", () => {
    const lines = Array.from({ length: 20 }, (_, i) => `line ${i}`);
    const chunks = chunkLines(lines, { chunkLines: 60, overlap: 10 });
    expect(chunks).toHaveLength(1);
    expect(chunks[0].lineStart).toBe(1);
    expect(chunks[0].lineEnd).toBe(20);
    expect(chunks[0].text).toContain("line 0");
  });

  it("overlaps chunks by the requested line count", () => {
    const lines = Array.from({ length: 120 }, (_, i) => `l${i}`);
    const chunks = chunkLines(lines, { chunkLines: 60, overlap: 10 });
    expect(chunks.map((c) => [c.lineStart, c.lineEnd])).toEqual([
      [1, 60], [51, 110], [101, 120],
    ]);
    // overlap region (51-60) appears in both first and second chunk
    expect(chunks[0].text).toContain("l50");
    expect(chunks[1].text).toContain("l50");
  });

  it("skips all-whitespace chunks", () => {
    const lines = ["a", "", "", "b"];
    const chunks = chunkLines(lines, { chunkLines: 2, overlap: 0 });
    expect(chunks.map((c) => c.text)).toEqual(["a\n", "b"]);
  });

  it("defaults to 60-line chunks with 10-line overlap", () => {
    const lines = Array.from({ length: 100 }, () => "x");
    const chunks = chunkLines(lines, {});
    expect(chunks).toHaveLength(2);
    expect(chunks[1].lineStart).toBe(51);
  });
});

describe("BM25", () => {
  it("scores documents containing query identifiers highest", () => {
    const corpus = [
      ["plugin", "runtime", "lifecycle", "start", "stop"],
      ["ui", "theme", "color", "button"],
    ];
    const bm25 = new BM25(corpus);
    const scores = bm25.score(["plugin", "runtime"]);
    expect(scores).toHaveLength(2);
    expect(scores[0]).toBeGreaterThan(0);
    expect(scores[1]).toBe(0);
    // manual Okapi BM25 with k1=1.5, b=0.75, N=2, avgdl=4.5
    // idf = ln(1 + (2-1+0.5)/(1+0.5)) = ln(2); tf=1, dl=5
    // denom = 0.25 + 0.75*(5/4.5) ; score = idf * 2.5 / (1 + 1.5*denom)
    expect(scores[0]).toBeCloseTo(1.3202, 3);
  });

  it("handles empty corpus and unmatched queries", () => {
    expect(new BM25([]).score(["x"])).toEqual([]);
    const bm25 = new BM25([["a"], ["b"]]);
    expect(bm25.score(["zzz"])[0]).toBe(0);
  });
});

describe("tfidfDenseScores", () => {
  it("ranks a doc sharing an ngram with the query above an unrelated doc", () => {
    const corpus = [
      "the plugin runtime manages the plugin lifecycle",
      "the ui theme renders colorful buttons",
    ];
    const scores = tfidfDenseScores(corpus, "plugin runtime lifecycle");
    expect(scores).toHaveLength(2);
    expect(scores[0]).toBeGreaterThan(0);
    expect(scores[1]).toBe(0);
    expect(scores[0]).toBeGreaterThan(scores[1]);
  });

  it("ranks a doc sharing a bigram higher than one sharing only a unigram", () => {
    const corpus = [
      "plugin runtime and lifecycle management",
      "plugin is a generic word used elsewhere",
    ];
    const scores = tfidfDenseScores(corpus, "plugin runtime");
    expect(scores[0]).toBeGreaterThan(scores[1]);
  });

  it("returns zeros for an empty corpus or a tokenless query", () => {
    expect(tfidfDenseScores([], "anything")).toEqual([]);
    expect(tfidfDenseScores(["a b c"], "!!")).toEqual([0]);
  });

  it("is deterministic across calls", () => {
    const corpus = ["alpha beta gamma", "beta gamma delta", "omega"];
    const a = tfidfDenseScores(corpus, "beta gamma");
    const b = tfidfDenseScores(corpus, "beta gamma");
    expect(a).toEqual(b);
  });
});

describe("reciprocalRankFusion", () => {
  it("fuses ranks with 1/(k + rank + 1) contributions", () => {
    // doc 0 first in BM25, second in dense; doc 1 the reverse
    const fused = reciprocalRankFusion([0, 1], [1, 0], 2, 60);
    expect(fused).toHaveLength(2);
    expect(fused[0].index).toBe(0);
    expect(fused[0].score).toBeCloseTo(1 / 61 + 1 / 62, 6);
    expect(fused[1].index).toBe(1);
    expect(fused[1].score).toBeCloseTo(1 / 62 + 1 / 61, 6);
  });

  it("agrees on tie scores and stays sorted descending", () => {
    const fused = reciprocalRankFusion([0, 1, 2], [2, 1, 0], 3, 60);
    for (let i = 1; i < fused.length; i += 1) {
      expect(fused[i - 1].score).toBeGreaterThanOrEqual(fused[i].score);
    }
  });
});

describe("crossEncoderScore", () => {
  it("scores an overlapping document above zero and an unrelated doc at zero", () => {
    const q = "plugin runtime lifecycle";
    expect(crossEncoderScore(q, "the plugin runtime manages the lifecycle of plugins")).toBeGreaterThan(0.5);
    expect(crossEncoderScore(q, "the ui paints colorful buttons")).toBe(0);
  });

  it("penalizes negation near a query term", () => {
    const q = "use react";
    const positive = crossEncoderScore(q, "we use react for rendering");
    const negated = crossEncoderScore(q, "we do not use react anymore");
    expect(negated).toBeLessThan(positive);
    expect(negated).toBeGreaterThanOrEqual(0);
  });

  it("returns 0 when either side has no tokens", () => {
    expect(crossEncoderScore("", "anything at all")).toBe(0);
    expect(crossEncoderScore("query words", "!!")).toBe(0);
  });
});

function makeChunks() {
  return [
    { cid: "a#L1-60", file: "src/core.ts", lineStart: 1, lineEnd: 60, text: "export class PluginRuntime {\n  lifecycle() { return start(); }\n}" },
    { cid: "b#L1-60", file: "src/ui.ts", lineStart: 1, lineEnd: 60, text: "export function renderTheme() { return 'colors'; }" },
    { cid: "c#L1-60", file: "docs/lifecycle.md", lineStart: 1, lineEnd: 60, text: "# Plugin lifecycle\n\nPlugins start and stop through the runtime lifecycle." },
  ];
}

describe("runRetrieval", () => {
  it("returns top_k results with per-stage scores", () => {
    const results = runRetrieval(makeChunks(), "plugin runtime lifecycle", { topK: 2 });
    expect(results).toHaveLength(2);
    for (const r of results) {
      expect(r.file).toBeTruthy();
      expect(r.lineStart).toBeGreaterThan(0);
      expect(r.lineEnd).toBeGreaterThanOrEqual(r.lineStart);
      expect(typeof r.ceScore).toBe("number");
      expect(typeof r.rrfScore).toBe("number");
      expect(r.finalRank).toBeGreaterThan(0);
    }
    // semantic hits outrank the unrelated ui file
    expect(results[0].file).not.toBe("src/ui.ts");
    expect(results.some((r) => r.file === "src/core.ts" || r.file === "docs/lifecycle.md")).toBe(true);
  });

  it("is deterministic: same query, same chunks, same result", () => {
    const a = runRetrieval(makeChunks(), "plugin runtime lifecycle", { topK: 5 });
    const b = runRetrieval(makeChunks(), "plugin runtime lifecycle", { topK: 5 });
    expect(a).toEqual(b);
  });

  it("respects the candidate pool and top_k bounds", () => {
    const results = runRetrieval(makeChunks(), "plugin", { topK: 1, candidatePool: 2 });
    expect(results).toHaveLength(1);
  });
});

let tmpDir;
let engine;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "files-search-test-"));
  engine = new RetrievalEngine(tmpDir);
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

async function writeSearchWorkspace() {
  await fs.mkdir(path.join(tmpDir, "src"));
  await fs.mkdir(path.join(tmpDir, "docs"));
  await fs.writeFile(
    path.join(tmpDir, "src", "core.ts"),
    [
      "export class PluginRuntime {",
      "  start() { return this.lifecycle('start'); }",
      "  stop() { return this.lifecycle('stop'); }",
      "  lifecycle(mode) { return mode; }",
      "}",
      "// the plugin runtime lifecycle is managed here",
    ].join("\n"),
  );
  await fs.writeFile(
    path.join(tmpDir, "src", "ui.ts"),
    [
      "export function renderTheme() {",
      "  return { color: 'red', padding: 8 };",
      "}",
    ].join("\n"),
  );
  await fs.writeFile(
    path.join(tmpDir, "docs", "lifecycle.md"),
    "# Plugin lifecycle\n\nPlugins are started and stopped through the runtime lifecycle coordinator.\n",
  );
}

describe("RetrievalEngine.searchRelevant", () => {
  it("ranks semantic matches above unrelated files and returns the expected shape", async () => {
    await writeSearchWorkspace();
    const result = await engine.searchRelevant({ query: "plugin runtime lifecycle", topK: 3 });
    expect(result.query).toBe("plugin runtime lifecycle");
    // unrelated files (src/ui.ts) are filtered out, so only the 2 relevant
    // chunks are returned even when topK is larger
    expect(result.results).toHaveLength(2);
    const paths = result.results.map((r) => r.path);
    expect(paths).not.toContain("src/ui.ts");
    expect(paths.some((p) => p === "src/core.ts" || p === "docs/lifecycle.md")).toBe(true);
    for (const r of result.results) {
      expect(typeof r.path).toBe("string");
      expect(Number.isInteger(r.line)).toBe(true);
      expect(Number.isInteger(r.lineEnd)).toBe(true);
      expect(typeof r.score).toBe("number");
      expect(typeof r.snippet).toBe("string");
    }
    expect(result.meta.filesScanned).toBeGreaterThanOrEqual(3);
    expect(result.meta.chunks).toBeGreaterThanOrEqual(1);
    expect(result.meta.timingMs.total).toBeGreaterThanOrEqual(0);
  });

  it("scopes retrieval to a subdirectory via path", async () => {
    await writeSearchWorkspace();
    const result = await engine.searchRelevant({ query: "plugin runtime lifecycle", topK: 5, path: "src" });
    expect(result.results.length).toBeGreaterThan(0);
    for (const r of result.results) {
      expect(r.path.startsWith("src/")).toBe(true);
    }
  });

  it("respects top_k and returns deterministic results across calls", async () => {
    await writeSearchWorkspace();
    const one = await engine.searchRelevant({ query: "plugin runtime lifecycle", topK: 1 });
    expect(one.results).toHaveLength(1);
    const again = await engine.searchRelevant({ query: "plugin runtime lifecycle", topK: 1 });
    expect(again.results).toEqual(one.results);
  });

  it("reuses the chunk cache on repeat calls and bypasses it with refresh", async () => {
    await writeSearchWorkspace();
    const first = await engine.searchRelevant({ query: "plugin runtime lifecycle", topK: 3 });
    expect(first.meta.cacheMisses).toBeGreaterThan(0);
    const second = await engine.searchRelevant({ query: "plugin runtime lifecycle", topK: 3 });
    expect(second.meta.cacheHits).toBeGreaterThan(0);
    expect(second.meta.cacheMisses).toBe(0);
    expect(second.results).toEqual(first.results);
    const refreshed = await engine.searchRelevant({ query: "plugin runtime lifecycle", topK: 3, refresh: true });
    expect(refreshed.meta.cacheHits).toBe(0);
  });

  it("throws on a missing or empty query", async () => {
    await writeSearchWorkspace();
    await expect(engine.searchRelevant({})).rejects.toThrow(/query/i);
    await expect(engine.searchRelevant({ query: "   " })).rejects.toThrow(/query/i);
  });

  it("skips vendored and build directories", async () => {
    await fs.mkdir(path.join(tmpDir, "node_modules"), { recursive: true });
    await fs.mkdir(path.join(tmpDir, "dist"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "node_modules", "junk.ts"), "plugin runtime lifecycle junk\n");
    await fs.writeFile(path.join(tmpDir, "dist", "bundle.js"), "plugin runtime lifecycle bundle\n");
    await writeSearchWorkspace();
    const result = await engine.searchRelevant({ query: "plugin runtime lifecycle", topK: 5 });
    for (const r of result.results) {
      expect(r.path.startsWith("node_modules/")).toBe(false);
      expect(r.path.startsWith("dist/")).toBe(false);
    }
  });
});
