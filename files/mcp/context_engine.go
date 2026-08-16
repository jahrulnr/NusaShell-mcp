package main

import (
	"encoding/json"
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

// Workspace Context Engine — deterministic, LLM-free repo map builder.
// Port of files/mcp/context-engine.js (Aider-style repo map):
//   1. Stack classification & manifest detection
//   2. Directory walking with .gitignore/.ignore matching
//   3. Regex fallback-lexer symbol extraction (definitions + references)
//   4. Directed dependency graph + Personalized PageRank
//   5. Scope-aware elision + token budget fitting (binary search)
//   6. In-memory tag cache with (path, mtime, size) invalidation

const (
	maxExtractBytes        = 1 * 1024 * 1024
	maxDefsPerFile         = 30
	maxScanFiles           = 20000
	maxWorkspaceInstrBytes = 50 * 1024
	activeFileBoost        = 50.0
	queryMatchBoost        = 10.0
	roleMatchBoost         = 8.0
	recencyHalfLifeMs      = float64(30 * 86400 * 1000)
	mathLn2                = 0.6931471805599453
	workspaceInstrURI      = "nusashell://workspace/AGENTS.md"
)

var manifests = map[string]string{
	"package.json":        "node",
	"pnpm-workspace.yaml": "node",
	"Cargo.toml":          "rust",
	"pyproject.toml":      "python",
	"requirements.txt":    "python",
	"go.mod":              "go",
	"composer.json":       "php",
	"pom.xml":             "java-maven",
	"build.gradle":        "java-gradle",
	"mix.exs":             "elixir",
	"Gemfile":             "ruby",
}

var supportedExts = map[string]string{
	".ts": "typescript", ".tsx": "typescript", ".js": "javascript",
	".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".py": "python", ".go": "go", ".rs": "rust", ".rb": "ruby",
	".php": "php", ".java": "java", ".kt": "kotlin", ".ex": "elixir",
	".exs": "elixir", ".cs": "csharp", ".cpp": "cpp", ".cc": "cpp",
	".cxx": "cpp", ".h": "cpp", ".hpp": "cpp", ".c": "c",
}

var docExts = map[string]bool{".md": true, ".mdx": true, ".txt": true, ".rst": true}

var keyDepPrefixes = []string{
	"react", "next", "vue", "nuxt", "svelte", "express", "fastify",
	"electron", "vitest", "jest", "typescript", "zod", "fastapi",
	"django", "flask", "axum", "tokio",
}

var defaultIgnoreDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"target": true, "dist": true, "build": true, ".next": true,
	".nuxt": true, ".cache": true, ".turbo": true, "coverage": true,
	".vitest": true, "out": true, "__pycache__": true, ".pytest_cache": true,
	".mypy_cache": true, ".ruff_cache": true, "vendor": true, ".venv": true,
	"venv": true, "env": true, ".idea": true, ".vscode": true,
}

var ignoreFileNames = []string{".gitignore", ".ignore"}

var identRe = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)

var keywords = func() map[string]bool {
	words := "if else for while return break continue switch case default try catch finally throw new class interface type enum function def func fn const let var import from export async await public private protected static abstract final this self super true false null none undefined void int string bool boolean number any unknown never struct union module package namespace use pub mut impl trait match when do end then elif and or not is in as with yield lambda fun data object val by companion"
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}()

// defPattern encapsulates a definition regex per language.
type defPattern struct {
	kind string
	re   *regexp.Regexp
}

func defPatternsFor(lang string) []defPattern {
	raw := map[string][]struct {
		kind string
		re   string
	}{
		"typescript": {
			{"type", `^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?(?:class|interface|type|enum)\s+([A-Za-z_$][\w$]*)`},
			{"function", `^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\s*\(`},
			{"function", `^\s*(?:export\s+)?(?:async\s+)?([A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\s*\([^)]*\)\s*[:{]`},
			{"const", `^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`},
		},
		"javascript": {
			{"type", `^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`},
			{"function", `^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(`},
			{"const", `^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`},
		},
		"python": {
			{"type", `^\s*class\s+([A-Za-z_]\w*)`},
			{"function", `^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`},
			{"const", `^\s*([A-Z_][A-Z0-9_]*)\s*=`},
		},
		"go": {
			{"function", `^\s*func\s+(?:\([^)]*\)\s+)?([A-Za-z_]\w*)\s*\(`},
			{"type", `^\s*type\s+([A-Za-z_]\w*)\s+`},
			{"const", `^\s*const\s+([A-Za-z_]\w*)\s*=`},
		},
		"rust": {
			{"function", `^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`},
			{"type", `^\s*(?:pub\s+)?(?:struct|enum|trait)\s+([A-Za-z_]\w*)`},
			{"const", `^\s*(?:pub\s+)?const\s+([A-Za-z_]\w*)\s*:`},
		},
		"php": {
			{"type", `^\s*(?:final\s+|abstract\s+)?class\s+([A-Za-z_]\w*)`},
			{"function", `^\s*function\s+([A-Za-z_]\w*)\s*\(`},
		},
		"ruby": {
			{"type", `^\s*(?:class|module)\s+([A-Za-z_]\w*)`},
			{"function", `^\s*def\s+([A-Za-z_][\w]*)`},
		},
		"java": {
			{"type", `^\s*(?:public|private|protected)?\s*(?:abstract\s+|final\s+)*class\s+([A-Za-z_]\w*)`},
			{"function", `^\s*(?:public|private|protected)\s+(?:static\s+)?(?:[\w<>\[\]]+\s+)+([A-Za-z_]\w*)\s*\(`},
		},
		"csharp": {
			{"type", `^\s*(?:public|private|protected|internal)?\s*(?:class|interface|struct|enum)\s+([A-Za-z_]\w*)`},
		},
		"cpp": {
			{"type", `^\s*(?:class|struct|enum\s+class|enum)\s+([A-Za-z_]\w*)`},
		},
		"c": {
			{"type", `^\s*(?:struct|enum|union)\s+([A-Za-z_]\w*)`},
		},
		"elixir": {
			{"function", `^\s*defp?\s+([a-z_]\w*)`},
			{"type", `^\s*defmodule\s+([A-Za-z_.\w]*)`},
		},
		"kotlin": {
			{"function", `^\s*fun\s+([A-Za-z_]\w*)`},
			{"type", `^\s*(?:data\s+)?(?:class|object)\s+([A-Za-z_]\w*)`},
		},
	}
	var out []defPattern
	for _, d := range raw[lang] {
		if re, err := regexp.Compile(d.re); err == nil {
			out = append(out, defPattern{kind: d.kind, re: re})
		}
	}
	return out
}

func estimateTokens(text string) int {
	n := len(text) / 4
	if n < 1 {
		return 1
	}
	return n
}

// personalizedPagerank implements Personalized PageRank via power iteration.
func personalizedPagerank(nodes []string, outEdges map[string][]string, personalization map[string]float64, damping float64, maxIter int, tol float64) map[string]float64 {
	n := len(nodes)
	if n == 0 {
		return map[string]float64{}
	}
	idx := map[string]int{}
	for i, f := range nodes {
		idx[f] = i
	}
	p := make([]float64, n)
	psum := 0.0
	for i, f := range nodes {
		v := 1.0
		if m, ok := personalization[f]; ok {
			v = m
		}
		p[i] = v
		psum += v
	}
	if psum > 0 {
		for i := range p {
			p[i] /= psum
		}
	} else {
		for i := range p {
			p[i] = 1 / float64(n)
		}
	}
	rank := make([]float64, n)
	for i := range rank {
		rank[i] = 1 / float64(n)
	}
	outDeg := make([]int, n)
	for i, f := range nodes {
		outDeg[i] = len(outEdges[f])
	}
	for iter := 0; iter < maxIter; iter++ {
		next := make([]float64, n)
		for i := range next {
			next[i] = (1 - 0.85) * p[i]
		}
		dangling := 0.0
		for i := 0; i < n; i++ {
			if outDeg[i] == 0 {
				dangling += rank[i]
			}
		}
		if dangling > 0 {
			for i := 0; i < n; i++ {
				next[i] += 0.85 * dangling * p[i]
			}
		}
		for i := 0; i < n; i++ {
			deg := outDeg[i]
			if deg == 0 {
				continue
			}
			share := (0.85 * rank[i]) / float64(deg)
			for _, target := range outEdges[nodes[i]] {
				next[idx[target]] += share
			}
		}
		delta := 0.0
		for i := 0; i < n; i++ {
			d := next[i] - rank[i]
			if d < 0 {
				d = -d
			}
			delta += d
		}
		rank = next
		if delta < 1e-6 {
			break
		}
	}
	scores := map[string]float64{}
	for i, f := range nodes {
		scores[f] = rank[i]
	}
	return scores
}

func escapeRegExp(text string) string {
	re := regexp.MustCompile(`[.+^${}()|[\]\\]`)
	return re.ReplaceAllString(text, `\$0`)
}

// matchesIgnore implements minimal gitignore-style matching over posix rel paths.
func matchesIgnore(relPosix string, patterns []string) bool {
	parts := strings.Split(relPosix, "/")
	name := parts[len(parts)-1]
	for _, raw := range patterns {
		pat := raw
		if strings.HasSuffix(pat, "/") {
			pat = pat[:len(pat)-1]
		}
		if strings.HasPrefix(pat, "/") {
			pat = pat[1:]
			if relPosix == pat || strings.HasPrefix(relPosix, pat+"/") {
				return true
			}
			continue
		}
		if strings.Contains(pat, "*") {
			escaped := ""
			for i, piece := range strings.Split(pat, "*") {
				if i > 0 {
					escaped += ".*"
				}
				escaped += escapeRegExp(piece)
			}
			if re, err := regexp.Compile("^" + escaped + "$"); err == nil {
				if re.MatchString(name) || re.MatchString(relPosix) {
					return true
				}
			}
		} else if name == pat {
			return true
		} else {
			for _, part := range parts {
				if part == pat {
					return true
				}
			}
		}
	}
	return false
}

func readIgnorePatterns(dir string) []string {
	var patterns []string
	for _, name := range ignoreFileNames {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			patterns = append(patterns, trimmed)
		}
	}
	return patterns
}

type walkFile struct {
	abs  string
	rel  string
	lang string
	doc  bool
}

type walkStats struct {
	VisitedDirs     int `json:"visitedDirs"`
	ConsideredFiles int `json:"consideredFiles"`
	IgnoredFiles    int `json:"ignoredFiles"`
	CodeFiles       int `json:"codeFiles"`
	DocFiles        int `json:"docFiles"`
}

type defTag struct {
	Name string
	Kind string
	Sig  string
}

type symbolTagsEntry struct {
	mtimeMs int64
	size    int64
	lang    string
	defs    []defTag
	refs    []string
}

// ContextEngine builds token-budgeted workspace repo maps for one root.
type ContextEngine struct {
	mu    sync.Mutex
	root  string
	cache map[string]symbolTagsEntry
}

func NewContextEngine(root string) *ContextEngine {
	return &ContextEngine{root: filepath.Clean(root), cache: map[string]symbolTagsEntry{}}
}

func (e *ContextEngine) SetRoot(newRoot string) {
	e.root = filepath.Clean(newRoot)
	e.cache = map[string]symbolTagsEntry{}
}

func toPosixPath(p string) string {
	return filepath.ToSlash(p)
}

func round6(v float64) float64 {
	return math.Floor(v*1e6+0.5) / 1e6
}

// recencyDecay: 1 at now, ~0.5 at one half-life.
func recencyDecay(mtimeMs, nowMs float64) float64 {
	if mtimeMs <= 0 {
		return 1
	}
	age := nowMs - mtimeMs
	if age < 0 {
		age = 0
	}
	return math.Exp(-mathLn2 * age / recencyHalfLifeMs)
}

func isTestPath(rel string) bool {
	p := filepath.ToSlash(rel)
	if regexp.MustCompile(`(?:^|/)(?:tests?|__tests__)/`).MatchString(p) {
		return true
	}
	return regexp.MustCompile(`\.(?:test|spec)\.[^.]+$`).MatchString(p)
}

func isConventionBase(base string) bool {
	l := strings.ToLower(base)
	return l == "agents.md" || strings.HasPrefix(l, "rules")
}

func isDocExt(rel string) bool {
	return docExts[strings.ToLower(filepath.Ext(rel))]
}

func isConventionPath(rel string) bool {
	p := filepath.ToSlash(rel)
	if strings.ToLower(filepath.Base(p)) == "agents.md" || strings.HasPrefix(strings.ToLower(filepath.Base(p)), "rules") {
		return true
	}
	return strings.Contains(p, "/docs/")
}

// roleMatchMultiplier maps role x path to a PageRank personalization boost.
func roleMatchMultiplier(rel, role string) float64 {
	switch role {
	case "planner":
		if isDocExt(rel) || isConventionPath(rel) {
			return roleMatchBoost
		}
	case "executor":
		ext := strings.ToLower(filepath.Ext(rel))
		_, isCode := supportedExts[ext]
		if isCode && !isTestPath(rel) && !isConventionBase(filepath.Base(rel)) {
			return roleMatchBoost
		}
	case "reviewer":
		if isTestPath(rel) || isConventionBase(filepath.Base(rel)) || isConventionPath(rel) {
			return roleMatchBoost
		}
		if isDocExt(rel) {
			return roleMatchBoost / 2
		}
	}
	return 1
}

// readWorkspaceInstructions returns the workspace-root AGENTS.md as context.
func (e *ContextEngine) readWorkspaceInstructions() (map[string]any, bool) {
	filePath := filepath.Join(e.root, "AGENTS.md")
	stat, err := os.Stat(filePath)
	if err != nil || !stat.Mode().IsRegular() {
		return nil, false
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, false
	}
	text := string(raw)
	if len(raw) > maxWorkspaceInstrBytes {
		text = string(raw[:maxWorkspaceInstrBytes]) + "\n\n[AGENTS.md truncated by the Files MCP resource limit.]"
	}
	return map[string]any{
		"uri":         workspaceInstrURI,
		"name":        "Workspace instructions",
		"description": "Workspace-root AGENTS.md project guidance.",
		"mimeType":    "text/markdown",
		"text":        text,
	}, true
}

// detectStack classifies the workspace from manifests at root + one level.
func (e *ContextEngine) DetectStack(subPath string) (map[string]any, error) {
	base := resolvePath(e.root, subPath)
	info := map[string]any{
		"category":    "documentation",
		"languages":   []string{},
		"manifests":   map[string]string{},
		"projectName": "",
		"version":     "",
		"keyDeps":     []string{},
		"isMonorepo":  false,
	}
	rootManifests := map[string]string{}
	nestedManifests := map[string]string{}
	for name, kind := range manifests {
		if _, err := os.Stat(filepath.Join(base, name)); err == nil {
			rootManifests[name] = kind
		}
	}
	children, err := os.ReadDir(base)
	if err != nil {
		children = nil
	}
	for _, child := range children {
		if !child.IsDir() || strings.HasPrefix(child.Name(), ".") {
			continue
		}
		for name, kind := range manifests {
			if _, err := os.Stat(filepath.Join(base, child.Name(), name)); err == nil {
				nestedManifests[child.Name()+"/"+name] = kind
			}
		}
	}
	all := map[string]string{}
	for k, v := range rootManifests {
		all[k] = v
	}
	for k, v := range nestedManifests {
		all[k] = v
	}
	info["manifests"] = all
	monorepo := len(nestedManifests) > 0
	if _, err := os.Stat(filepath.Join(base, "pnpm-workspace.yaml")); err == nil {
		monorepo = true
	}
	info["isMonorepo"] = monorepo

	if data, err := os.ReadFile(filepath.Join(base, "package.json")); err == nil {
		var pkg struct {
			Name            string            `json:"name"`
			Version         string            `json:"version"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
			Scripts         map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			info["projectName"] = pkg.Name
			info["version"] = pkg.Version
			var keyDeps []string
			add := func(deps map[string]string) {
				for d := range deps {
					for _, prefix := range keyDepPrefixes {
						if strings.HasPrefix(d, prefix) {
							keyDeps = append(keyDeps, d)
							break
						}
					}
				}
			}
			add(pkg.Dependencies)
			add(pkg.DevDependencies)
			sort.Strings(keyDeps)
			if len(keyDeps) > 20 {
				keyDeps = keyDeps[:20]
			}
			info["keyDeps"] = keyDeps
			scripts := map[string]string{}
			keys := make([]string, 0, len(pkg.Scripts))
			for k := range pkg.Scripts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i >= 10 {
					break
				}
				scripts[k] = pkg.Scripts[k]
			}
			info["scripts"] = scripts
		}
	}
	kindToLang := map[string]string{
		"node": "typescript", "rust": "rust", "python": "python", "go": "go",
		"php": "php", "elixir": "elixir", "ruby": "ruby",
	}
	langSet := map[string]bool{}
	for _, kind := range all {
		if strings.HasPrefix(kind, "java") {
			langSet["java"] = true
		} else if lang, ok := kindToLang[kind]; ok {
			langSet[lang] = true
		}
	}
	languages := make([]string, 0, len(langSet))
	for l := range langSet {
		languages = append(languages, l)
	}
	sort.Strings(languages)
	info["languages"] = languages
	if len(rootManifests) > 0 {
		if monorepo {
			info["category"] = "hybrid"
		} else {
			info["category"] = "coding"
		}
	} else if len(nestedManifests) > 0 {
		info["category"] = "hybrid"
	}
	return info, nil
}

// walkWorkspace walks the tree respecting default ignores + .gitignore/.ignore.
func (e *ContextEngine) WalkWorkspace(base string, maxFiles int) ([]walkFile, walkStats) {
	if maxFiles <= 0 {
		maxFiles = maxScanFiles
	}
	var files []walkFile
	var stats walkStats
	rootPatterns := readIgnorePatterns(base)
	type qitem struct {
		dir      string
		patterns []string
	}
	queue := []qitem{{dir: base, patterns: rootPatterns}}
	seenDirs := map[string]bool{}
	for len(queue) > 0 && len(files) < maxFiles {
		item := queue[0]
		queue = queue[1:]
		if seenDirs[item.dir] {
			continue
		}
		seenDirs[item.dir] = true
		stats.VisitedDirs++
		entries, err := os.ReadDir(item.dir)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		localPatterns := append(append([]string{}, item.patterns...), readIgnorePatterns(item.dir)...)

		for _, entry := range entries {
			if len(files) >= maxFiles {
				break
			}
			abs := filepath.Join(item.dir, entry.Name())
			rel := relativePosix(e.root, abs, entry.Name())
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".") || defaultIgnoreDirs[entry.Name()] {
					continue
				}
				if matchesIgnore(rel, localPatterns) {
					continue
				}
				queue = append(queue, qitem{dir: abs, patterns: localPatterns})
				continue
			}
			if !entry.Type().IsRegular() {
				continue
			}
			if strings.HasPrefix(entry.Name(), ".") {
				isIgnore := false
				for _, n := range ignoreFileNames {
					if entry.Name() == n {
						isIgnore = true
						break
					}
				}
				if !isIgnore {
					continue
				}
			}
			stats.ConsideredFiles++
			if matchesIgnore(rel, localPatterns) {
				stats.IgnoredFiles++
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if lang, ok := supportedExts[ext]; ok {
				files = append(files, walkFile{abs: abs, rel: rel, lang: lang})
				stats.CodeFiles++
			} else if docExts[ext] {
				files = append(files, walkFile{abs: abs, rel: rel, doc: true})
				stats.DocFiles++
			}
		}
	}
	return files, stats
}

// extractFromText extracts definition and reference tags from source text.
func (e *ContextEngine) extractFromText(rel, text, lang string) ([]defTag, []string) {
	var defs []defTag
	var refs []string
	seenDefs := map[string]bool{}
	patterns := defPatternsFor(lang)
	for _, line := range splitLines(text) {
		for _, pat := range patterns {
			m := pat.re.FindStringSubmatch(line)
			if m != nil && len(m) > 1 && !seenDefs[m[1]] {
				seenDefs[m[1]] = true
				sig := strings.TrimRight(line, " \t\r\n")
				if len(sig) > 120 {
					sig = sig[:117] + "..."
				}
				defs = append(defs, defTag{Name: m[1], Kind: pat.kind, Sig: sig})
				break
			}
		}
		for _, tok := range identRe.FindAllString(line, -1) {
			if len(tok) < 2 || keywords[tok] || seenDefs[tok] {
				continue
			}
			refs = append(refs, tok)
		}
	}
	return defs, refs
}

// extractAll extracts symbols for walked files using the mtime/size cache.
func (e *ContextEngine) ExtractAll(files []walkFile, refresh bool) (map[string][]defTag, map[string][]string, int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cacheHits, cacheMisses := 0, 0
	defsByFile := map[string][]defTag{}
	refsByFile := map[string][]string{}
	for _, file := range files {
		stat, err := os.Stat(file.abs)
		if err != nil {
			continue
		}
		if file.doc {
			cached, ok := e.cache[file.rel]
			if !refresh && ok && cached.mtimeMs == stat.ModTime().UnixMilli() && cached.size == stat.Size() {
				cacheHits++
			} else {
				cacheMisses++
				e.cache[file.rel] = symbolTagsEntry{mtimeMs: stat.ModTime().UnixMilli(), size: stat.Size(), lang: "doc"}
			}
			defsByFile[file.rel] = nil
			refsByFile[file.rel] = nil
			continue
		}
		if stat.Size() > maxExtractBytes {
			continue
		}
		cached, ok := e.cache[file.rel]
		if !refresh && ok && cached.mtimeMs == stat.ModTime().UnixMilli() && cached.size == stat.Size() {
			cacheHits++
			defsByFile[file.rel] = cached.defs
			refsByFile[file.rel] = cached.refs
			continue
		}
		cacheMisses++
		raw, err := os.ReadFile(file.abs)
		if err != nil {
			continue
		}
		defs, refs := e.extractFromText(file.rel, string(raw), file.lang)
		e.cache[file.rel] = symbolTagsEntry{mtimeMs: stat.ModTime().UnixMilli(), size: stat.Size(), lang: file.lang, defs: defs, refs: refs}
		defsByFile[file.rel] = defs
		refsByFile[file.rel] = refs
	}
	return defsByFile, refsByFile, cacheHits, cacheMisses
}

type rankPair struct {
	rel   string
	score float64
}

type rankOutput struct {
	ranked     []rankPair
	graphStats map[string]int
	roleScores []map[string]any
}

// rankFiles builds the directed graph and ranks files with Personalized PageRank.
func (e *ContextEngine) rankFiles(defsByFile map[string][]defTag, refsByFile map[string][]string, activeFile, query, role string, mtimes map[string]int64, now int64) rankOutput {
	symbolToDefiners := map[string][]string{}
	nodesSet := map[string]bool{}
	for rel, defs := range defsByFile {
		nodesSet[rel] = true
		for _, d := range defs {
			found := false
			for _, f := range symbolToDefiners[d.Name] {
				if f == rel {
					found = true
					break
				}
			}
			if !found {
				symbolToDefiners[d.Name] = append(symbolToDefiners[d.Name], rel)
			}
		}
	}
	outEdges := map[string][]string{}
	for rel, refs := range refsByFile {
		nodesSet[rel] = true
		for _, ref := range refs {
			for _, definer := range symbolToDefiners[ref] {
				if definer == rel {
					continue
				}
				exists := false
				for _, t := range outEdges[rel] {
					if t == definer {
						exists = true
						break
					}
				}
				if !exists {
					outEdges[rel] = append(outEdges[rel], definer)
				}
			}
		}
	}
	nodes := make([]string, 0, len(nodesSet))
	for n := range nodesSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	useRole := role == "planner" || role == "executor" || role == "reviewer"
	personalization := map[string]float64{}
	if activeFile != "" {
		personalization[toPosixPath(activeFile)] = activeFileBoost
	}
	var terms []string
	for _, raw := range regexp.MustCompile(`[^A-Za-z0-9_$]+`).Split(strings.ToLower(query), -1) {
		if len(raw) >= 2 {
			terms = append(terms, raw)
		}
	}
	if len(terms) > 0 {
		for rel, defs := range defsByFile {
			for _, d := range defs {
				lower := strings.ToLower(d.Name)
				for _, t := range terms {
					if strings.Contains(lower, t) {
						personalization[rel] *= queryMatchBoost
						goto matched
					}
				}
			}
		matched:
		}
	}
	if useRole {
		for _, rel := range nodes {
			if mult := roleMatchMultiplier(rel, role); mult != 1 {
				personalization[rel] *= mult
			}
		}
	}
	scores := personalizedPagerank(nodes, outEdges, personalization, 0.85, 100, 1e-6)

	ranked := make([]rankPair, 0, len(nodes))
	var roleScores []map[string]any
	for _, rel := range nodes {
		score := scores[rel]
		if useRole {
			clock := now
			if clock == 0 {
				clock = time.Now().UnixMilli()
			}
			mtimeMs := float64(clock)
			if m, ok := mtimes[rel]; ok && m > 0 {
				mtimeMs = float64(m)
			}
			recency := recencyDecay(mtimeMs, float64(clock))
			rm := roleMatchMultiplier(rel, role)
			score *= recency
			defCost := 1
			if defs := defsByFile[rel]; len(defs) > 0 {
				n := len(defs)
				if n > maxDefsPerFile {
					n = maxDefsPerFile
				}
				sum := 0
				for _, d := range defs[:n] {
					sum += estimateTokens(d.Sig)
				}
				if sum > 1 {
					defCost = sum
				}
			}
			roleScores = append(roleScores, map[string]any{
				"path": rel, "score": round6(score), "cost": defCost,
				"roleMatch": rm, "recency": round6(recency),
			})
		}
		ranked = append(ranked, rankPair{rel: rel, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].rel < ranked[j].rel
	})
	if useRole {
		sort.SliceStable(roleScores, func(i, j int) bool {
			a, b := roleScores[i], roleScores[j]
			if a["score"].(float64) != b["score"].(float64) {
				return a["score"].(float64) > b["score"].(float64)
			}
			return a["path"].(string) < b["path"].(string)
		})
	}
	edgeCount := 0
	for _, targets := range outEdges {
		edgeCount += len(targets)
	}
	return rankOutput{ranked: ranked, graphStats: map[string]int{"nodes": len(nodes), "edges": edgeCount}, roleScores: roleScores}
}

// buildRepoMap renders the markdown repo map within the token budget via
// binary search over the number of ranked files included.
func buildRepoMap(ranked []rankPair, defsByFile map[string][]defTag, budget int, activeFile string) map[string]any {
	type renderedItem struct {
		rel   string
		score float64
		sigs  []string
	}
	var rendered []renderedItem
	for _, pair := range ranked {
		defs := defsByFile[pair.rel]
		if len(defs) > maxDefsPerFile {
			defs = defs[:maxDefsPerFile]
		}
		sigs := make([]string, 0, len(defs))
		for _, d := range defs {
			sigs = append(sigs, d.Sig)
		}
		rendered = append(rendered, renderedItem{rel: pair.rel, score: pair.score, sigs: sigs})
	}
	render := func(k int) (string, int, int, int) {
		var lines []string
		lines = append(lines, "# Workspace Context Map", "")
		if activeFile != "" {
			lines = append(lines, "_active file: `"+activeFile+"`_", "")
		}
		filesShown, symbolsShown := 0, 0
		if k > len(rendered) {
			k = len(rendered)
		}
		for _, item := range rendered[:k] {
			filesShown++
			lines = append(lines, fmt.Sprintf("## `%s`  (rank %.4f)", item.rel, item.score))
			if len(item.sigs) == 0 {
				lines = append(lines, "_no top-level definitions_")
			}
			for _, sig := range item.sigs {
				symbolsShown++
				lines = append(lines, "- "+sig+" ⋮")
			}
			lines = append(lines, "")
		}
		md := strings.Join(lines, "\n")
		return md, filesShown, symbolsShown, estimateTokens(md)
	}
	if len(rendered) == 0 {
		md, fs, ss, tokens := render(0)
		return map[string]any{"map": md, "filesShown": fs, "symbolsShown": ss, "tokensUsed": tokens, "elidedBodies": ss}
	}
	lo, hi := 1, len(rendered)
	bestMd, bestFs, bestSs, bestTokens := render(lo)
	if bestTokens > budget {
		return map[string]any{"map": bestMd, "filesShown": bestFs, "symbolsShown": bestSs, "tokensUsed": bestTokens, "elidedBodies": bestSs}
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		md, fs, ss, tokens := render(mid)
		if tokens <= budget {
			bestMd, bestFs, bestSs, bestTokens = md, fs, ss, tokens
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return map[string]any{"map": bestMd, "filesShown": bestFs, "symbolsShown": bestSs, "tokensUsed": bestTokens, "elidedBodies": bestSs}
}

// allocateRoleBudget applies role-specific token budget multipliers.
func allocateRoleBudget(base int, role string) int {
	switch role {
	case "planner":
		return base*12/10 + 256
	case "executor":
		return base*105/100 + 64
	case "reviewer":
		return base*112/100 + 128
	}
	return base
}

// ContextMap runs the full pipeline (classify, walk, extract, rank, elide, fit).
func (e *ContextEngine) ContextMap(subPath string, budget int, activeFile, query, role string, maxFiles int, refresh bool) (map[string]any, error) {
	useRole := role == "planner" || role == "executor" || role == "reviewer"
	effectiveBudget := allocateRoleBudget(budget, role)
	started := time.Now()
	base := resolvePath(e.root, subPath)

	t := time.Now()
	stack, err := e.DetectStack(subPath)
	if err != nil {
		return nil, err
	}
	classifyMs := time.Since(t)

	t = time.Now()
	files, walkStats := e.WalkWorkspace(base, maxFiles)
	walkMs := time.Since(t)

	t = time.Now()
	defsByFile, refsByFile, cacheHits, cacheMisses := e.ExtractAll(files, refresh)
	extractMs := time.Since(t)

	var mtimes map[string]int64
	if useRole {
		mtimes = map[string]int64{}
		for _, file := range files {
			entry, ok := e.cache[file.rel]
			if ok && entry.mtimeMs > 0 {
				mtimes[file.rel] = entry.mtimeMs
			}
		}
	}

	t = time.Now()
	ro := e.rankFiles(defsByFile, refsByFile, activeFile, query, role, mtimes, 0)
	graphMs := time.Since(t)

	t = time.Now()
	mapResult := buildRepoMap(ro.ranked, defsByFile, effectiveBudget, activeFile)
	elideMs := time.Since(t)

	ranks := make([][2]any, 0, 20)
	for i, pair := range ro.ranked {
		if i >= 20 {
			break
		}
		ranks = append(ranks, [2]any{pair.rel, round6(pair.score)})
	}
	statsMap := map[string]any{
		"tokensUsed":   mapResult["tokensUsed"],
		"filesShown":   mapResult["filesShown"],
		"symbolsShown": mapResult["symbolsShown"],
		"filesScanned": walkStats.CodeFiles + walkStats.DocFiles,
		"cacheHits":    cacheHits,
		"cacheMisses":  cacheMisses,
		"walk":         walkStats,
		"graph":        ro.graphStats,
		"timingMs": map[string]any{
			"classify": round2(classifyMs),
			"walk":     round2(walkMs),
			"extract":  round2(extractMs),
			"graph":    round2(graphMs),
			"elide":    round2(elideMs),
			"total":    round2(time.Since(started)),
		},
	}
	if useRole {
		statsMap["role"] = role
		statsMap["effectiveBudget"] = effectiveBudget
	}
	result := map[string]any{
		"map":   mapResult["map"],
		"stack": stack,
		"ranks": ranks,
		"stats": statsMap,
	}
	if useRole && ro.roleScores != nil {
		result["roleScores"] = ro.roleScores[:min(20, len(ro.roleScores))]
	}
	return result, nil
}

func round2(d time.Duration) float64 {
	return math.Round(d.Seconds()*10000) / 100
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ListSymbols lists definitions for one file or top-ranked files matching query.
func (e *ContextEngine) ListSymbols(filePath, query string, limit int) (map[string]any, error) {
	if filePath != "" {
		abs := resolvePath(e.root, filePath)
		lang, ok := supportedExts[strings.ToLower(filepath.Ext(abs))]
		if !ok {
			return nil, fmt.Errorf("Unsupported file type for symbol extraction: %s", filePath)
		}
		stat, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !stat.Mode().IsRegular() {
			return nil, fmt.Errorf("Not a file: %s", filePath)
		}
		if stat.Size() > maxExtractBytes {
			return nil, fmt.Errorf("File too large for symbol extraction (max 1 MB): %s", filePath)
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		rel := relativePosix(e.root, abs, filePath)
		defs, _ := e.extractFromText(rel, string(raw), lang)
		symbols := make([]map[string]any, 0, len(defs))
		for _, d := range defs {
			symbols = append(symbols, map[string]any{"name": d.Name, "line": 0, "kind": d.Kind, "signature": d.Sig})
		}
		return map[string]any{"path": filePath, "language": lang, "symbols": symbols}, nil
	}
	if query == "" {
		return nil, fmt.Errorf("list_symbols requires either a file path or a query")
	}
	files, _ := e.WalkWorkspace(e.root, 0)
	defsByFile, refsByFile, _, _ := e.ExtractAll(files, false)
	ro := e.rankFiles(defsByFile, refsByFile, "", query, "", nil, 0)
	lowered := strings.ToLower(query)
	var matches []map[string]any
	for _, pair := range ro.ranked {
		if len(matches) >= limit {
			break
		}
		var defs []map[string]any
		for _, d := range defsByFile[pair.rel] {
			if strings.Contains(strings.ToLower(d.Name), lowered) {
				defs = append(defs, map[string]any{"name": d.Name, "line": 0, "kind": d.Kind, "signature": d.Sig})
			}
		}
		if len(defs) == 0 {
			continue
		}
		matches = append(matches, map[string]any{"path": pair.rel, "rank": round6(pair.score), "symbols": defs})
	}
	return map[string]any{"query": query, "limit": limit, "files": matches}, nil
}
