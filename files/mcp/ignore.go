package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ignoreFileNames are gitignore-style files loaded while walking.
var ignoreFileNames = []string{".gitignore", ".ignore"}

// vcsDirNames are always skipped, even when a .gitignore does not list them.
// Git never tracks .git; ripgrep and similar tools hard-skip it too.
var vcsDirNames = map[string]bool{
	".git": true,
	".hg":  true,
	".svn": true,
}

// defaultIgnoredDirs are dependency/build output directories that a plain
// grep, search, or tree walk must not descend into.
var defaultIgnoredDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	"__pycache__":  true,
	"coverage":     true,
	"venv":         true,
	"out":          true,
}

// isDefaultIgnored reports whether a walked entry is skipped by default:
// VCS metadata (.git), other hidden entries, and well-known
// dependency/build directories. Only entries DISCOVERED during the walk
// are filtered — passing a hidden or vendored directory explicitly as the
// path root still searches inside it.
func isDefaultIgnored(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if vcsDirNames[name] {
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return defaultIgnoredDirs[name]
}

// ignoreRule is one gitignore-style pattern scoped to the directory that
// contained the .gitignore / .ignore file.
type ignoreRule struct {
	base    string
	pattern string
	negated bool
}

// walkIgnore holds inherited gitignore rules for a walk rooted at root.
type walkIgnore struct {
	root  string
	rules []ignoreRule
}

func newWalkIgnore(root string) *walkIgnore {
	return &walkIgnore{root: root, rules: collectIgnoreRules(root)}
}

func (w *walkIgnore) child(dir string) *walkIgnore {
	extra := parseIgnoreRules(dir)
	if len(extra) == 0 {
		return w
	}
	merged := make([]ignoreRule, 0, len(w.rules)+len(extra))
	merged = append(merged, w.rules...)
	merged = append(merged, extra...)
	return &walkIgnore{root: w.root, rules: merged}
}

// skip reports whether a child entry discovered during the walk should
// be omitted. The walk root itself is never passed here.
func (w *walkIgnore) skip(name, abs string) bool {
	if isDefaultIgnored(name) {
		return true
	}
	return w.gitIgnored(abs)
}

func (w *walkIgnore) gitIgnored(abs string) bool {
	if w == nil || len(w.rules) == 0 {
		return false
	}
	ignored := false
	for _, rule := range w.rules {
		rel, err := filepath.Rel(rule.base, abs)
		if err != nil || rel == "." {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "" || strings.HasPrefix(rel, "../") || rel == ".." {
			continue
		}
		if patternMatchesIgnore(rel, rule.pattern) {
			ignored = !rule.negated
		}
	}
	return ignored
}

// findRepoRoot walks up from dir looking for a .git entry (directory or
// file, so worktrees count). If none is found, dir itself is the ignore
// root — we do not inherit gitignore files from unrelated parent folders.
func findRepoRoot(dir string) string {
	current := filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(dir)
		}
		current = parent
	}
}

func collectIgnoreRules(dir string) []ignoreRule {
	dir = filepath.Clean(dir)
	root := findRepoRoot(dir)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return parseIgnoreRules(dir)
	}
	var rules []ignoreRule
	acc := root
	rules = append(rules, parseIgnoreRules(acc)...)
	if rel == "." {
		return rules
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		acc = filepath.Join(acc, part)
		rules = append(rules, parseIgnoreRules(acc)...)
	}
	return rules
}

func parseIgnoreRules(dir string) []ignoreRule {
	var rules []ignoreRule
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
			negated := false
			if strings.HasPrefix(trimmed, "!") {
				negated = true
				trimmed = strings.TrimSpace(trimmed[1:])
			}
			if trimmed == "" {
				continue
			}
			rules = append(rules, ignoreRule{
				base:    dir,
				pattern: trimmed,
				negated: negated,
			})
		}
	}
	return rules
}

func readIgnorePatterns(dir string) []string {
	rules := parseIgnoreRules(dir)
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		p := r.pattern
		if r.negated {
			p = "!" + p
		}
		out = append(out, p)
	}
	return out
}

// matchesIgnore implements gitignore-style matching over posix rel paths
// using last-match-wins so a later !pattern can re-include a file.
func matchesIgnore(relPosix string, patterns []string) bool {
	ignored := false
	for _, raw := range patterns {
		pat := raw
		negated := false
		if strings.HasPrefix(pat, "!") {
			negated = true
			pat = pat[1:]
		}
		if patternMatchesIgnore(relPosix, pat) {
			ignored = !negated
		}
	}
	return ignored
}

func patternMatchesIgnore(relPosix, raw string) bool {
	relPosix = strings.TrimPrefix(relPosix, "./")
	if relPosix == "" || raw == "" {
		return false
	}
	pat := raw
	if strings.HasSuffix(pat, "/") {
		pat = strings.TrimSuffix(pat, "/")
	}
	anchored := false
	if strings.HasPrefix(pat, "/") {
		pat = strings.TrimPrefix(pat, "/")
		anchored = true
	}
	parts := strings.Split(relPosix, "/")
	name := parts[len(parts)-1]

	if anchored {
		return relPosix == pat || strings.HasPrefix(relPosix, pat+"/")
	}
	if strings.Contains(pat, "*") {
		re, err := globIgnoreRegexp(pat)
		if err != nil {
			return false
		}
		if re.MatchString(name) || re.MatchString(relPosix) {
			return true
		}
		// *.log matches nested/dir/foo.log via basename; also match any
		// trailing path suffix so src/*.tmp works relative to the rule base.
		return false
	}
	if relPosix == pat || strings.HasPrefix(relPosix, pat+"/") || strings.HasSuffix(relPosix, "/"+pat) {
		return true
	}
	if !strings.Contains(pat, "/") {
		for _, part := range parts {
			if part == pat {
				return true
			}
		}
	}
	return name == pat
}

func globIgnoreRegexp(pat string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')
	for i, piece := range strings.Split(pat, "*") {
		if i > 0 {
			b.WriteString(".*")
		}
		b.WriteString(regexp.QuoteMeta(piece))
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}
