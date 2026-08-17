package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hybrid Retrieval (BM25 + TF-IDF dense proxy) with Reciprocal Rank Fusion
// and Cross-Encoder reranking. Port of files/mcp/search-relevant.js
// (hybrid_rag.py algorithm #2) to dependency-free Go.

const (
	retrievalSkipDirRe    = `(^|/)\.`
	retrievalChunkLines   = 60
	retrievalChunkOverlap = 10
	rrfK                  = 60
	snippetLimit          = 1200
	retrievalMaxFileBytes = 256 * 1024
	retrievalMaxFiles     = 20000
)

var retrievalSkipDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, ".git": true,
	".next": true, "out": true, "coverage": true, ".cache": true,
	".turbo": true, "tmp": true, "vendor": true, "assets": true,
	"fixtures": true, "snapshots": true,
}

var retrievalExts = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true,
	".cjs": true, ".py": true, ".md": true, ".go": true, ".txt": true,
}

var junkPat = regexp.MustCompile(`(\.min\.js$|\.bundle\.js$|-bundle\.|index-[A-Za-z0-9_-]{6,}\.js$|\.umd\.js$|vendor\.)`)

// tokenize splits text into lowercased tokens; snake_case splits into parts.
func tokenize(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, word := range regexp.MustCompile(`[^A-Za-z0-9_]+`).Split(text, -1) {
		if word == "" {
			continue
		}
		pieces := strings.Split(word, "_")
		for i, piece := range pieces {
			t := strings.ToLower(piece)
			// Attach a leading underscore to the following piece ("_x" stays "_x").
			if i < len(pieces)-1 && piece == "" {
				continue
			}
			if i > 0 && pieces[i-1] == "" && !strings.HasPrefix(t, "_") {
				t = "_" + t
			}
			if len(t) >= 2 && (t[0] >= 'a' && t[0] <= 'z' || t[0] == '_') {
				out = append(out, t)
			}
		}
	}
	return out
}

type retrievalChunk struct {
	cid       string
	file      string
	lineStart int
	lineEnd   int
	text      string
	tokens    []string
}

// chunkText splits a file's lines into overlapping line-based chunks.
func chunkText(lines []string) []retrievalChunk {
	chunkSize := retrievalChunkLines
	overlap := retrievalChunkOverlap
	step := chunkSize - overlap
	if step < 1 {
		step = 1
	}
	var chunks []retrievalChunk
	for i := 0; i < len(lines); {
		end := i + chunkSize
		if end > len(lines) {
			end = len(lines)
		}
		text := strings.Join(lines[i:end], "\n")
		trimmed := strings.TrimLeft(text, " \t")
		// JS: text.replace(/^\s+/, "") — trims only leading whitespace.
		if trimmed != "" {
			chunks = append(chunks, retrievalChunk{
				lineStart: i + 1,
				lineEnd:   end,
				text:      trimmed,
			})
		}
		i += step
	}
	return chunks
}

// BM25 implements Okapi BM25 (k1=1.5, b=0.75).
type bm25 struct {
	k1, b   float64
	nDocs   int
	docLens []int
	avgdl   float64
	tf      []map[string]int
	idf     map[string]float64
}

func newBM25(corpusTokens [][]string, k1, b float64) *bm25 {
	m := &bm25{k1: k1, b: b, nDocs: len(corpusTokens)}
	m.docLens = make([]int, len(corpusTokens))
	total := 0
	for i, toks := range corpusTokens {
		m.docLens[i] = len(toks)
		total += len(toks)
	}
	if m.nDocs > 0 {
		m.avgdl = float64(total) / float64(m.nDocs)
	}
	m.tf = make([]map[string]int, len(corpusTokens))
	df := map[string]int{}
	for i, toks := range corpusTokens {
		freq := map[string]int{}
		for _, t := range toks {
			freq[t]++
		}
		m.tf[i] = freq
		for t := range freq {
			df[t]++
		}
	}
	m.idf = map[string]float64{}
	for t, d := range df {
		m.idf[t] = math.Log(1 + (float64(m.nDocs)-float64(d)+0.5)/(float64(d)+0.5))
	}
	return m
}

func (m *bm25) score(queryTokens []string) []float64 {
	scores := make([]float64, m.nDocs)
	for i := 0; i < m.nDocs; i++ {
		dl := float64(m.docLens[i])
		denomNorm := (1 - m.b) + m.b*(dl/m.avgdl)
		freq := m.tf[i]
		var s float64
		for _, qt := range queryTokens {
			f := float64(freq[qt])
			if f == 0 {
				continue
			}
			idf := m.idf[qt]
			s += (idf * (f * (m.k1 + 1))) / (f + m.k1*denomNorm)
		}
		scores[i] = s
	}
	return scores
}

// tfidfDenseScores: TF-IDF (1-2 ngrams, sublinear TF, L2-normalized) + cosine.
func tfidfDenseScores(corpusTexts []string, query string, docTokens [][]string) []float64 {
	n := len(corpusTexts)
	if n == 0 {
		return nil
	}
	qTokens := tokenize(query)
	if len(qTokens) == 0 {
		return make([]float64, n)
	}
	toNgrams := func(tokens []string) []string {
		nGrams := append([]string{}, tokens...)
		for i := 0; i+1 < len(tokens); i++ {
			nGrams = append(nGrams, tokens[i]+" "+tokens[i+1])
		}
		return nGrams
	}
	docTokenLists := docTokens
	if docTokenLists == nil {
		docTokenLists = make([][]string, n)
		for i, text := range corpusTexts {
			docTokenLists[i] = tokenize(text)
		}
	}
	docNgrams := make([][]string, n)
	for i, toks := range docTokenLists {
		docNgrams[i] = toNgrams(toks)
	}
	qNgrams := toNgrams(qTokens)

	df := map[string]int{}
	for _, ngrams := range docNgrams {
		seen := map[string]bool{}
		for _, term := range ngrams {
			if !seen[term] {
				seen[term] = true
				df[term]++
			}
		}
	}
	idf := map[string]float64{}
	for term, d := range df {
		idf[term] = math.Log((1+float64(n))/(1+float64(d))) + 1
	}
	buildVector := func(ngrams []string) map[string]float64 {
		tf := map[string]int{}
		for _, term := range ngrams {
			tf[term]++
		}
		vec := map[string]float64{}
		for term, count := range tf {
			vec[term] = (1 + math.Log(float64(count))) * idf[term]
		}
		norm := 0.0
		for _, w := range vec {
			norm += w * w
		}
		norm = math.Sqrt(norm)
		if norm > 0 {
			for term, w := range vec {
				vec[term] = w / norm
			}
		}
		return vec
	}
	docVectors := make([]map[string]float64, n)
	for i, ngrams := range docNgrams {
		docVectors[i] = buildVector(ngrams)
	}
	qVec := buildVector(qNgrams)
	qNorm := 0.0
	for _, w := range qVec {
		qNorm += w * w
	}
	qNorm = math.Sqrt(qNorm)
	out := make([]float64, n)
	for i, vec := range docVectors {
		if qNorm == 0 || len(vec) == 0 {
			continue
		}
		dot := 0.0
		for term, w := range vec {
			if qw, ok := qVec[term]; ok {
				dot += w * qw
			}
		}
		out[i] = dot
	}
	return out
}

// reciprocalRankFusion: RRF(d) = sum 1/(k + rank_i(d)); tie-break by index.
func reciprocalRankFusion(bm25Ranked, denseRanked []int, nDocs int, k float64) []struct {
	index int
	score float64
} {
	rrf := make([]float64, nDocs)
	for rank, docIdx := range bm25Ranked {
		rrf[docIdx] += 1 / (k + float64(rank) + 1)
	}
	for rank, docIdx := range denseRanked {
		rrf[docIdx] += 1 / (k + float64(rank) + 1)
	}
	var out []struct {
		index int
		score float64
	}
	for i, s := range rrf {
		if s > 0 {
			out = append(out, struct {
				index int
				score float64
			}{i, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].index < out[j].index
	})
	return out
}

var negWords = map[string]bool{
	"not": true, "never": true, "deprecated": true, "avoid": true,
	"removed": true, "deleted": true, "disabled": true,
}

// crossEncoderScore: weighted n-gram overlap mimicking self-attention.
func crossEncoderScore(query, docText string) float64 {
	qTokens := tokenize(query)
	dTokens := tokenize(docText)
	if len(qTokens) == 0 || len(dTokens) == 0 {
		return 0
	}
	dSet := map[string]bool{}
	for _, t := range dTokens {
		dSet[t] = true
	}
	uni := 0.0
	for _, t := range qTokens {
		if dSet[t] {
			uni++
		}
	}
	uni /= float64(len(qTokens))

	qBigrams := map[string]bool{}
	for i := 0; i+1 < len(qTokens); i++ {
		qBigrams[qTokens[i]+" "+qTokens[i+1]] = true
	}
	dBigrams := map[string]bool{}
	for i := 0; i+1 < len(dTokens); i++ {
		dBigrams[dTokens[i]+" "+dTokens[i+1]] = true
	}
	bi := 0.0
	if len(qBigrams) > 0 {
		inter := 0
		for bg := range qBigrams {
			if dBigrams[bg] {
				inter++
			}
		}
		bi = float64(inter) / float64(len(qBigrams))
	}

	earlyThreshold := len(dTokens) / 3
	if earlyThreshold < 1 {
		earlyThreshold = 1
	}
	earlyTokens := map[string]bool{}
	for _, t := range dTokens[:earlyThreshold] {
		earlyTokens[t] = true
	}
	earlyBoost := 0.0
	for _, t := range qTokens {
		if earlyTokens[t] {
			earlyBoost++
		}
	}
	earlyBoost /= float64(len(qTokens))

	negPenalty := 0.0
	window := 8
	for i, t := range dTokens {
		if !negWords[t] {
			continue
		}
		lo := i - window
		if lo < 0 {
			lo = 0
		}
		hi := i + window
		if hi > len(dTokens) {
			hi = len(dTokens)
		}
		nearbyQ := 0
		for _, qt := range qTokens {
			for j := lo; j < hi; j++ {
				if dTokens[j] == qt {
					nearbyQ++
					break
				}
			}
		}
		if nearbyQ > 0 {
			negPenalty += 0.05 * float64(nearbyQ)
		}
	}
	if negPenalty > 0.3 {
		negPenalty = 0.3
	}

	density := 0.0
	for _, t := range qTokens {
		if dSet[t] {
			density++
		}
	}
	density /= math.Max(1, math.Sqrt(float64(len(dTokens))))

	score := 0.45*uni + 0.25*bi + 0.20*earlyBoost - negPenalty + 0.10*density
	if score < 0 {
		return 0
	}
	return score
}

func argSortDesc(scores []float64) []int {
	idx := make([]int, len(scores))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		if scores[idx[i]] != scores[idx[j]] {
			return scores[idx[i]] > scores[idx[j]]
		}
		return idx[i] < idx[j]
	})
	return idx
}

type retrievalResult struct {
	file      string
	lineStart int
	lineEnd   int
	ceScore   float64
	rrfScore  float64
	finalRank int
	snippet   string
}

// runRetrieval runs the full hybrid retrieval + rerank pipeline.
func runRetrieval(chunks []retrievalChunk, query string, topK, candidatePool int) []retrievalResult {
	if topK < 1 {
		topK = 1
	}
	if topK > 20 {
		topK = 20
	}
	if len(chunks) == 0 {
		return nil
	}
	qTokens := tokenize(query)
	if len(qTokens) == 0 {
		return nil
	}
	corpusTokens := make([][]string, len(chunks))
	for i, c := range chunks {
		if c.tokens != nil {
			corpusTokens[i] = c.tokens
		} else {
			corpusTokens[i] = tokenize(c.text)
		}
	}
	b := newBM25(corpusTokens, 1.5, 0.75)
	bm25Scores := b.score(qTokens)
	dense := tfidfDenseScores(nil, query, corpusTokens)
	if len(dense) != len(chunks) {
		dense = make([]float64, len(chunks))
		for i, c := range chunks {
			_ = c
			dense[i] = 0
		}
	}
	fused := reciprocalRankFusion(argSortDesc(bm25Scores), argSortDesc(dense), len(chunks), rrfK)
	pool := fused
	if candidatePool > 0 && len(pool) > candidatePool {
		pool = pool[:candidatePool]
	}
	type scored struct {
		chunk    retrievalChunk
		rrfScore float64
		ceScore  float64
	}
	var reranked []scored
	for _, item := range pool {
		ce := crossEncoderScore(query, chunks[item.index].text)
		if ce <= 0 {
			continue
		}
		reranked = append(reranked, scored{chunk: chunks[item.index], rrfScore: item.score, ceScore: ce})
	}
	sort.SliceStable(reranked, func(i, j int) bool {
		if reranked[i].ceScore != reranked[j].ceScore {
			return reranked[i].ceScore > reranked[j].ceScore
		}
		if reranked[i].rrfScore != reranked[j].rrfScore {
			return reranked[i].rrfScore > reranked[j].rrfScore
		}
		return reranked[i].chunk.cid < reranked[j].chunk.cid
	})
	if len(reranked) > topK {
		reranked = reranked[:topK]
	}
	out := make([]retrievalResult, 0, len(reranked))
	for i, r := range reranked {
		snippet := r.chunk.text
		if len(snippet) > snippetLimit {
			snippet = snippet[:snippetLimit] + "\n… [truncated]"
		}
		out = append(out, retrievalResult{
			file: r.chunk.file, lineStart: r.chunk.lineStart, lineEnd: r.chunk.lineEnd,
			ceScore: r.ceScore, rrfScore: r.rrfScore, finalRank: i + 1, snippet: snippet,
		})
	}
	return out
}

// RetrievalEngine indexes a workspace directory into mtime-cached chunks.
type RetrievalEngine struct {
	mu    sync.RWMutex
	root  string
	cache map[string]retrievalCacheEntry
}

type retrievalCacheEntry struct {
	mtimeMs int64
	size    int64
	chunks  []retrievalChunk
}

func NewRetrievalEngine(root string) *RetrievalEngine {
	return &RetrievalEngine{root: filepath.Clean(root), cache: map[string]retrievalCacheEntry{}}
}

func (e *RetrievalEngine) SetRoot(root string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.root = filepath.Clean(root)
	e.cache = map[string]retrievalCacheEntry{}
}

func (e *RetrievalEngine) searchRelevant(query string, topK int, scope string, refresh bool) map[string]any {
	return e.searchRelevantContext(context.Background(), query, topK, scope, refresh)
}

func (e *RetrievalEngine) searchRelevantContext(ctx context.Context, query string, topK int, scope string, refresh bool) map[string]any {
	t0 := time.Now()
	q := strings.TrimSpace(query)
	if q == "" {
		return map[string]any{"error": "searchRelevant requires a non-empty query"}
	}
	if err := ctx.Err(); err != nil {
		return map[string]any{"error": err.Error()}
	}
	e.mu.RLock()
	root := e.root
	e.mu.RUnlock()
	searchRoot := root
	if scope != "" {
		resolved, err := resolvePath(scope)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		searchRoot = resolved
	}
	chunks, filesScanned, cacheHits, cacheMisses := e.loadChunksContext(ctx, searchRoot, refresh)
	if err := ctx.Err(); err != nil {
		return map[string]any{"error": err.Error()}
	}

	k := topK
	if k < 1 {
		k = 1
	}
	if k > 20 {
		k = 20
	}
	pool := chunks
	if len(pool) > 100 {
		pool = pool[:100]
	}
	results := runRetrieval(chunks, q, k, 100)
	type res struct {
		Path    string  `json:"path"`
		Line    int     `json:"line"`
		LineEnd int     `json:"lineEnd"`
		Score   float64 `json:"score"`
		Snippet string  `json:"snippet"`
	}
	out := make([]res, 0, len(results))
	for _, r := range results {
		out = append(out, res{Path: r.file, Line: r.lineStart, LineEnd: r.lineEnd, Score: r.ceScore, Snippet: r.snippet})
	}
	return map[string]any{
		"query":   q,
		"results": out,
		"meta": map[string]any{
			"filesScanned": filesScanned,
			"chunks":       len(chunks),
			"cacheHits":    cacheHits,
			"cacheMisses":  cacheMisses,
			"timingMs":     map[string]any{"total": time.Since(t0).Seconds() * 1000},
		},
	}
}

func (e *RetrievalEngine) loadChunks(dir string, refresh bool) ([]retrievalChunk, int, int, int) {
	return e.loadChunksContext(context.Background(), dir, refresh)
}

// loadChunksContext is concurrency-safe and cooperatively cancellable. Refresh
// bypasses the cache for this scan instead of clearing shared state; clearing a
// shared cache while another request is reading it was the source of a Go map
// race in the original port.
func (e *RetrievalEngine) loadChunksContext(ctx context.Context, dir string, refresh bool) ([]retrievalChunk, int, int, int) {
	var chunks []retrievalChunk
	filesScanned, cacheHits, cacheMisses := 0, 0, 0

	var walk func(string) error
	walk = func(current string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".") || retrievalSkipDirs[entry.Name()] {
					continue
				}
				if err := walk(filepath.Join(current, entry.Name())); err != nil {
					return err
				}
				continue
			}
			if !entry.Type().IsRegular() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if !retrievalExts[ext] {
				continue
			}
			if junkPat.MatchString(entry.Name()) {
				continue
			}
			full := filepath.Join(current, entry.Name())
			rel := absPath(full, entry.Name())
			filesScanned++
			stat, err := os.Stat(full)
			if err != nil {
				continue
			}
			if stat.Size() > retrievalMaxFileBytes {
				continue
			}
			e.mu.RLock()
			cached, ok := e.cache[rel]
			e.mu.RUnlock()
			if !refresh && ok && cached.mtimeMs == stat.ModTime().UnixMilli() && cached.size == stat.Size() {
				cacheHits++
				chunks = append(chunks, cached.chunks...)
				continue
			}
			cacheMisses++
			if err := ctx.Err(); err != nil {
				return err
			}
			raw, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			fileChunks := chunkText(splitLines(string(raw)))
			for i := range fileChunks {
				fileChunks[i].cid = rel + fmt.Sprintf("#L%d-%d", fileChunks[i].lineStart, fileChunks[i].lineEnd)
				fileChunks[i].file = rel
				fileChunks[i].tokens = tokenize(fileChunks[i].text)
			}
			e.mu.Lock()
			e.cache[rel] = retrievalCacheEntry{mtimeMs: stat.ModTime().UnixMilli(), size: stat.Size(), chunks: fileChunks}
			e.mu.Unlock()
			chunks = append(chunks, fileChunks...)
		}
		return nil
	}
	_ = walk(dir)
	return chunks, filesScanned, cacheHits, cacheMisses
}
