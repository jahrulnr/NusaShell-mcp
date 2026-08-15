/**
 * search-relevant.js — Hybrid Retrieval (BM25 + TF-IDF dense proxy) with
 * Reciprocal Rank Fusion and Cross-Encoder reranking.
 *
 * Port of `.experimental/hybrid_rag.py` (algorithm #2 from
 * "Optimasi Konteks AI IDE.md") to dependency-free JS so the Files MCP can
 * expose semantic "search_relevant" without shipping numpy/sklearn.
 *
 * Pipeline (two-stage retrieval):
 *   Stage 1 (maximize recall): BM25 over code chunks (exact identifiers,
 *     error codes, API names) fused with a TF-IDF + cosine proxy for a
 *     Bi-Encoder via Reciprocal Rank Fusion (k=60, standard smoothing).
 *   Stage 2 (maximize precision): Cross-Encoder proxy rerank — weighted
 *     n-gram overlap with position weighting and negation penalties.
 *   Output: top-k chunks with per-stage scores; chunks with zero
 *     cross-encoder overlap are filtered out entirely.
 *
 * All stages are pure and deterministic (no randomness), and the chunk
 * index is cached by file mtime+size so repeat queries are cheap.
 */

import fs from "node:fs/promises";
import path from "node:path";
import { relativePosix } from "./config.js";

const SKIP_DIRS = new Set([
  "node_modules", "dist", "build", ".git", ".next", "out", "coverage",
  ".cache", ".turbo", "tmp", "vendor", "assets", "fixtures", "snapshots",
]);
const EXTS = new Set([".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".md"]);
const JUNK_PAT = /(\.min\.js$|\.bundle\.js$|-bundle\.|index-[A-Za-z0-9_-]{6,}\.js$|\.umd\.js$|vendor\.)/i;
const MAX_FILE_BYTES = 256 * 1024; // 256 KB, mirrors hybrid_rag.py
const CHUNK_LINES = 60;
const CHUNK_OVERLAP = 10;
const RRF_K = 60;
const SNIPPET_LIMIT = 1200;

/**
 * Split text into lowercased tokens. Mirrors hybrid_rag.py's tokenizer with
 * one deliberate improvement: snake_case identifiers are split into their
 * word parts (`snake_case_name` → snake/case/name) so queries in either
 * convention match. camelCase is kept whole (`camelCaseName` →
 * camelcasename). Tokens shorter than 2 chars or not starting with a letter
 * or underscore are dropped.
 * @param {string} text
 * @returns {string[]}
 */
export function tokenize(text) {
  if (typeof text !== "string" || text.length === 0) return [];
  const out = [];
  for (const word of text.split(/[^A-Za-z0-9_]+/)) {
    if (!word) continue;
    const pieces = word.split(/_+/);
    for (let i = 0; i < pieces.length; i += 1) {
      let t = pieces[i].toLowerCase();
      if (t.length === 0) continue;
      // A leading empty piece means the word started with underscore(s);
      // attach the underscore to the following piece so "_x" stays "_x"
      // while "snake_case_name" still splits into word parts.
      if (i > 0 && pieces[i - 1] === "" && t[0] !== "_") {
        t = `_${t}`;
      }
      if (t.length >= 2 && /^[A-Za-z_]/.test(t)) out.push(t);
    }
  }
  return out;
}

/**
 * Split a file's lines into overlapping line-based chunks.
 * @param {string[]} lines
 * @param {{ chunkLines?: number, overlap?: number }} [options]
 * @returns {Array<{ lineStart: number, lineEnd: number, text: string }>}
 */
export function chunkLines(lines, options = {}) {
  const chunkSize = options.chunkLines ?? CHUNK_LINES;
  const overlap = options.overlap ?? CHUNK_OVERLAP;
  const step = Math.max(1, chunkSize - overlap);
  const chunks = [];
  let i = 0;
  while (i < lines.length) {
    const end = Math.min(i + chunkSize, lines.length);
    let text = lines.slice(i, end).join("\n");
    if (text.trim()) {
      // Drop leading whitespace (keeps trailing newline for clean display)
      // without shifting the reported line range.
      text = text.replace(/^\s+/, "");
      chunks.push({ lineStart: i + 1, lineEnd: end, text });
    }
    i += step;
  }
  return chunks;
}

/** Okapi BM25 (k1=1.5, b=0.75), port of hybrid_rag.py. */
export class BM25 {
  /**
   * @param {string[][]} corpusTokens
   * @param {number} [k1]
   * @param {number} [b]
   */
  constructor(corpusTokens, k1 = 1.5, b = 0.75) {
    this.k1 = k1;
    this.b = b;
    this.nDocs = corpusTokens.length;
    this.docLens = corpusTokens.map((d) => d.length);
    this.avgdl = this.nDocs
      ? this.docLens.reduce((a, n) => a + n, 0) / this.nDocs
      : 0;
    this.tf = [];
    const df = new Map();
    for (const tokens of corpusTokens) {
      const freq = new Map();
      for (const t of tokens) freq.set(t, (freq.get(t) ?? 0) + 1);
      this.tf.push(freq);
      for (const term of freq.keys()) df.set(term, (df.get(term) ?? 0) + 1);
    }
    this.idf = new Map();
    for (const [term, d] of df) {
      this.idf.set(term, Math.log(1 + (this.nDocs - d + 0.5) / (d + 0.5)));
    }
  }

  /**
   * @param {string[]} queryTokens
   * @returns {number[]}
   */
  score(queryTokens) {
    const scores = new Array(this.nDocs).fill(0);
    for (let i = 0; i < this.nDocs; i += 1) {
      const dl = this.docLens[i];
      const denomNorm = (1 - this.b) + this.b * (this.avgdl ? dl / this.avgdl : 0);
      const freq = this.tf[i];
      let s = 0;
      for (const qt of queryTokens) {
        const f = freq.get(qt) ?? 0;
        if (f === 0) continue;
        const idf = this.idf.get(qt) ?? 0;
        s += (idf * (f * (this.k1 + 1))) / (f + this.k1 * denomNorm);
      }
      scores[i] = s;
    }
    return scores;
  }
}

/**
 * TF-IDF (1-2 n-grams, sublinear TF, L2-normalized) + cosine similarity.
 * Deterministic proxy for a Bi-Encoder, port of hybrid_rag.py's dense_search
 * (sklearn TfidfVectorizer + cosine_similarity).
 * @param {string[]} corpusTexts
 * @param {string} query
 * @param {string[][]} [docTokens] pre-tokenized docs (skips re-tokenizing)
 * @returns {number[]}
 */
export function tfidfDenseScores(corpusTexts, query, docTokens) {
  if (!Array.isArray(corpusTexts) || corpusTexts.length === 0) return [];
  const qTokens = tokenize(query);
  if (qTokens.length === 0) return corpusTexts.map(() => 0);

  const toNgrams = (tokens) => {
    const ngrams = tokens.slice();
    for (let i = 0; i + 1 < tokens.length; i += 1) {
      ngrams.push(`${tokens[i]} ${tokens[i + 1]}`);
    }
    return ngrams;
  };

  const docTokenLists = docTokens ?? corpusTexts.map((text) => tokenize(text));
  const docNgrams = docTokenLists.map(toNgrams);
  const queryNgrams = toNgrams(qTokens);

  // Document frequency + smoothed IDF (sklearn: ln((1+n)/(1+df)) + 1).
  const df = new Map();
  for (const ngrams of docNgrams) {
    for (const term of new Set(ngrams)) df.set(term, (df.get(term) ?? 0) + 1);
  }
  const n = docNgrams.length;
  const idf = new Map();
  for (const [term, d] of df) idf.set(term, Math.log((1 + n) / (1 + d)) + 1);

  // Sublinear term frequency, weighted by IDF, L2-normalized.
  const buildVector = (ngrams) => {
    const tf = new Map();
    for (const term of ngrams) tf.set(term, (tf.get(term) ?? 0) + 1);
    const vec = new Map();
    for (const [term, count] of tf) {
      vec.set(term, (1 + Math.log(count)) * (idf.get(term) ?? 0));
    }
    let norm = 0;
    for (const w of vec.values()) norm += w * w;
    norm = Math.sqrt(norm);
    if (norm > 0) {
      for (const [term, w] of vec) vec.set(term, w / norm);
    }
    return vec;
  };

  const docVectors = docNgrams.map(buildVector);
  const queryVector = buildVector(queryNgrams);
  let qNorm = 0;
  for (const w of queryVector.values()) qNorm += w * w;
  qNorm = Math.sqrt(qNorm);

  return docVectors.map((vec) => {
    if (qNorm === 0 || vec.size === 0) return 0;
    let dot = 0;
    for (const [term, w] of vec) {
      const qw = queryVector.get(term);
      if (qw !== undefined) dot += w * qw;
    }
    return dot; // both vectors normalized → cosine similarity
  });
}

/**
 * Reciprocal Rank Fusion: RRF(d) = Σ 1/(k + rank_i(d)), k=60.
 * Returns { index, score } sorted descending; tie-breaks by index so the
 * ordering is deterministic.
 * @param {number[]} bm25Ranked
 * @param {number[]} denseRanked
 * @param {number} nDocs
 * @param {number} [k]
 * @returns {Array<{ index: number, score: number }>}
 */
export function reciprocalRankFusion(bm25Ranked, denseRanked, nDocs, k = RRF_K) {
  const rrf = new Array(nDocs).fill(0);
  bm25Ranked.forEach((docIdx, rank) => {
    rrf[docIdx] += 1 / (k + rank + 1);
  });
  denseRanked.forEach((docIdx, rank) => {
    rrf[docIdx] += 1 / (k + rank + 1);
  });
  return rrf
    .map((score, index) => ({ index, score }))
    .filter((x) => x.score > 0)
    .sort((a, b) => b.score - a.score || a.index - b.index);
}

const NEG_WORDS = new Set([
  "not", "never", "deprecated", "avoid", "removed", "deleted", "disabled",
]);

/**
 * Cross-Encoder proxy: weighted n-gram overlap mimicking full self-attention
 * (unigram + bigram overlap, early-position boost, negation penalty, density
 * normalization). Port of hybrid_rag.py's cross_encoder_score.
 * @param {string} query
 * @param {string} docText
 * @returns {number}
 */
export function crossEncoderScore(query, docText) {
  const qTokens = tokenize(query);
  const dTokens = tokenize(docText);
  if (qTokens.length === 0 || dTokens.length === 0) return 0;

  const dSet = new Set(dTokens);
  const uni = qTokens.reduce((acc, t) => acc + (dSet.has(t) ? 1 : 0), 0) / qTokens.length;

  const qBigrams = new Set();
  for (let i = 0; i + 1 < qTokens.length; i += 1) {
    qBigrams.add(`${qTokens[i]} ${qTokens[i + 1]}`);
  }
  const dBigrams = new Set();
  for (let i = 0; i + 1 < dTokens.length; i += 1) {
    dBigrams.add(`${dTokens[i]} ${dTokens[i + 1]}`);
  }
  let bi = 0;
  if (qBigrams.size > 0) {
    let inter = 0;
    for (const bg of qBigrams) if (dBigrams.has(bg)) inter += 1;
    bi = inter / qBigrams.size;
  }

  const earlyThreshold = Math.max(1, Math.floor(dTokens.length / 3));
  const earlyTokens = new Set(dTokens.slice(0, earlyThreshold));
  const earlyBoost = qTokens.reduce((acc, t) => acc + (earlyTokens.has(t) ? 1 : 0), 0) / qTokens.length;

  let negPenalty = 0;
  const windowSize = 8;
  for (let i = 0; i < dTokens.length; i += 1) {
    if (!NEG_WORDS.has(dTokens[i])) continue;
    const lo = Math.max(0, i - windowSize);
    const hi = Math.min(dTokens.length, i + windowSize);
    let nearbyQ = 0;
    for (const qt of qTokens) {
      for (let j = lo; j < hi; j += 1) {
        if (dTokens[j] === qt) {
          nearbyQ += 1;
          break;
        }
      }
    }
    if (nearbyQ > 0) negPenalty += 0.05 * nearbyQ;
  }
  negPenalty = Math.min(0.3, negPenalty);

  const density = qTokens.reduce((acc, t) => acc + (dSet.has(t) ? 1 : 0), 0)
    / Math.max(1, Math.sqrt(dTokens.length));

  const score = 0.45 * uni + 0.25 * bi + 0.20 * earlyBoost - negPenalty + 0.10 * density;
  return Math.max(0, score);
}

/**
 * @param {number[]} scores
 * @returns {number[]} indices sorted by score descending (ties by index)
 */
function argSortDesc(scores) {
  return scores
    .map((s, i) => [s, i])
    .sort((a, b) => b[0] - a[0] || a[1] - b[1])
    .map((pair) => pair[1]);
}

/**
 * Run the full hybrid retrieval + rerank pipeline over pre-built chunks.
 * @param {Array<{ cid: string, file: string, lineStart: number, lineEnd: number, text: string, tokens?: string[] }>} chunks
 * @param {string} query
 * @param {{ topK?: number, candidatePool?: number }} [options]
 * @returns {Array<{ file: string, lineStart: number, lineEnd: number, ceScore: number, rrfScore: number, finalRank: number, snippet: string }>}
 */
export function runRetrieval(chunks, query, options = {}) {
  const topK = Math.max(1, Math.min(20, options.topK ?? 5));
  const candidatePool = options.candidatePool ?? 100;
  if (chunks.length === 0) return [];
  const qTokens = tokenize(query);
  if (qTokens.length === 0) return [];

  // Stage 1: sparse (BM25) + dense (TF-IDF cosine) retrieval. Pre-tokenized
  // chunks (from the engine cache) skip re-tokenizing the whole corpus.
  const corpusTokens = chunks.map((c) => c.tokens ?? tokenize(c.text));
  const bm25 = new BM25(corpusTokens);
  const bm25Scores = bm25.score(qTokens);
  const denseScores = tfidfDenseScores(chunks.map((c) => c.text), query, corpusTokens);

  // Fusion: RRF over both ranked lists.
  const rrf = reciprocalRankFusion(
    argSortDesc(bm25Scores),
    argSortDesc(denseScores),
    chunks.length,
    RRF_K,
  );
  const pool = rrf.slice(0, candidatePool);

  // Stage 2: cross-encoder rerank; drop chunks with zero overlap.
  const reranked = pool
    .map((item) => ({
      chunk: chunks[item.index],
      rrfScore: item.score,
      ceScore: crossEncoderScore(query, chunks[item.index].text),
    }))
    .filter((r) => r.ceScore > 0)
    .sort((a, b) => b.ceScore - a.ceScore || b.rrfScore - a.rrfScore || a.chunk.cid.localeCompare(b.chunk.cid));

  return reranked.slice(0, topK).map((r, i) => {
    const text = r.chunk.text;
    const snippet = text.length > SNIPPET_LIMIT
      ? `${text.slice(0, SNIPPET_LIMIT)}\n… [truncated]`
      : text;
    return {
      file: r.chunk.file,
      lineStart: r.chunk.lineStart,
      lineEnd: r.chunk.lineEnd,
      ceScore: r.ceScore,
      rrfScore: r.rrfScore,
      finalRank: i + 1,
      snippet,
    };
  });
}

/**
 * RetrievalEngine indexes a workspace directory into mtime-cached chunks and
 * answers `searchRelevant` queries. Cache keys are file path + mtime + size,
 * so unchanged files are never re-read between queries.
 */
export class RetrievalEngine {
  /**
   * @param {string} root absolute workspace root
   */
  constructor(root) {
    this.root = root;
    /** @type {Map<string, { mtimeMs: number, size: number, chunks: Array<{ cid: string, file: string, lineStart: number, lineEnd: number, text: string, tokens: string[] }> }>} */
    this.cache = new Map();
  }

  /** Re-point the engine at a new root and drop the chunk cache. */
  setRoot(root) {
    this.root = root;
    this.cache.clear();
  }

  /**
   * Search the workspace for the most relevant files/chunks for a query.
   * @param {{ query: string, topK?: number, path?: string, refresh?: boolean }} options
   * @returns {Promise<{ query: string, results: Array<{ path: string, line: number, lineEnd: number, score: number, snippet: string }>, meta: { filesScanned: number, chunks: number, cacheHits: number, cacheMisses: number, timingMs: { total: number } } }>}
   */
  async searchRelevant({ query, topK = 5, path: scope = "", refresh = false }) {
    const t0 = performance.now();
    const q = typeof query === "string" ? query.trim() : "";
    if (!q) throw new Error("searchRelevant requires a non-empty query");
    const k = Math.max(1, Math.min(20, Number.isFinite(topK) ? topK : 5));

    const searchRoot = scope ? path.resolve(this.root, scope) : this.root;
    const { chunks, filesScanned, cacheHits, cacheMisses } =
      await this.#loadChunks(searchRoot, scope, refresh);

    const results = runRetrieval(chunks, q, {
      topK: k,
      candidatePool: Math.max(1, Math.min(100, chunks.length)),
    }).map((r) => ({
      path: r.file,
      line: r.lineStart,
      lineEnd: r.lineEnd,
      score: r.ceScore,
      snippet: r.snippet,
    }));

    return {
      query: q,
      results,
      meta: {
        filesScanned,
        chunks: chunks.length,
        cacheHits,
        cacheMisses,
        timingMs: { total: Math.max(0, performance.now() - t0) },
      },
    };
  }

  /**
   * Walk `dir`, chunk each eligible file, and serve chunks from the
   * mtime+size cache when a file is unchanged.
   * @param {string} dir
   * @param {string} scope
   * @param {boolean} refresh
   * @returns {Promise<{ chunks: Array<{ cid: string, file: string, lineStart: number, lineEnd: number, text: string }>, filesScanned: number, cacheHits: number, cacheMisses: number }>}
   */
  async #loadChunks(dir, scope, refresh) {
    if (refresh) this.cache.clear();
    const chunks = [];
    let filesScanned = 0;
    let cacheHits = 0;
    let cacheMisses = 0;

    const walk = async (currentDir) => {
      let entries;
      try {
        entries = await fs.readdir(currentDir, { withFileTypes: true });
      } catch {
        return; // unreadable dir (permissions, vanished) — skip silently
      }
      for (const entry of entries) {
        if (entry.isDirectory()) {
          if (entry.name.startsWith(".") || SKIP_DIRS.has(entry.name)) continue;
          await walk(path.join(currentDir, entry.name));
        } else if (entry.isFile()) {
          const ext = path.extname(entry.name).toLowerCase();
          if (!EXTS.has(ext)) continue;
          if (JUNK_PAT.test(entry.name)) continue;
          const full = path.join(currentDir, entry.name);
          // Workspace-relative paths are POSIX for agent/tool consumers (Windows
          // path.relative would otherwise emit backslashes and break filters).
          const rel = relativePosix(this.root, full);
          filesScanned += 1;
          let stat;
          try {
            stat = await fs.stat(full);
          } catch {
            continue;
          }
          if (stat.size > MAX_FILE_BYTES) continue;

          const cached = this.cache.get(rel);
          if (!refresh && cached && cached.mtimeMs === stat.mtimeMs && cached.size === stat.size) {
            cacheHits += 1;
            chunks.push(...cached.chunks);
            continue;
          }

          cacheMisses += 1;
          let text;
          try {
            text = await fs.readFile(full, "utf8");
          } catch {
            continue;
          }
          const fileChunks = chunkLines(text.split("\n"), {
            chunkLines: CHUNK_LINES,
            overlap: CHUNK_OVERLAP,
          }).map((c) => ({
            cid: `${rel}#L${c.lineStart}-${c.lineEnd}`,
            file: rel,
            lineStart: c.lineStart,
            lineEnd: c.lineEnd,
            text: c.text,
            // Pre-tokenize once at index time so repeat queries skip the
            // full-corpus regex pass (BM25 + dense both consume this).
            tokens: tokenize(c.text),
          }));
          this.cache.set(rel, { mtimeMs: stat.mtimeMs, size: stat.size, chunks: fileChunks });
          chunks.push(...fileChunks);
        }
      }
    };

    await walk(dir);
    return { chunks, filesScanned, cacheHits, cacheMisses };
  }
}
