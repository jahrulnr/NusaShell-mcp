package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxReadBytes     = 1 * 1024 * 1024
	maxTreeDepth     = 10
	maxSearchResults = 1000
	maxGrepLineLen   = 500
	magicByteSample  = 512
	binaryNulRatio   = 0.30
	maxGrepFileBytes = 32 * 1024 * 1024 // grep skips larger files (bounded I/O)
)

var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true, ".js": true,
	".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".less": true,
	".xml": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".cfg": true, ".conf": true, ".env": true, ".gitignore": true, ".svg": true,
	".csv": true, ".log": true, ".sh": true, ".bash": true, ".zsh": true,
	".fish": true, ".py": true, ".rb": true, ".go": true, ".rs": true,
	".java": true, ".kt": true, ".swift": true, ".c": true, ".cpp": true,
	".h": true, ".hpp": true, ".cs": true, ".php": true, ".pl": true,
	".lua": true, ".r": true, ".sql": true, ".graphql": true, ".gql": true,
	".vue": true, ".svelte": true, ".mod": true, ".sum": true, ".lock": true,
	".work": true,
}

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".webp": true, ".ico": true, ".tiff": true, ".tif": true, ".avif": true,
	".heic": true, ".heif": true,
}

var textBasenames = map[string]bool{
	"makefile": true, "dockerfile": true, "license": true, "readme": true,
	"changelog": true, "authors": true, "contributors": true, "todo": true,
	"notice": true, ".gitignore": true, ".gitattributes": true,
	".editorconfig": true, ".npmrc": true, ".env": true, ".env.local": true,
	".env.production": true, "go.mod": true, "go.sum": true, "go.work": true,
	"go.work.sum": true, ".bashrc": true, ".bash_profile": true, ".profile": true,
	".zshrc": true, "procfile": true, "gemfile": true, "rakefile": true,
	"vagrantfile": true, ".dockerignore": true, ".prettierrc": true,
	".eslintrc": true, ".babelrc": true,
}

type magicSig struct {
	offset int
	bytes  []byte
	ftype  string
}

var magicBytes = []magicSig{
	{0, []byte{0x89, 0x50, 0x4e, 0x47}, "image"},
	{0, []byte{0xff, 0xd8, 0xff}, "image"},
	{0, []byte{0x47, 0x49, 0x46, 0x38}, "image"},
	{0, []byte{0x42, 0x4d}, "image"},
	{0, []byte{0x25, 0x50, 0x44, 0x46}, "pdf"},
	{0, []byte{0x50, 0x4b, 0x03, 0x04}, "archive"},
	{0, []byte{0x1f, 0x8b}, "archive"},
	{257, []byte{0x75, 0x73, 0x74, 0x61, 0x72}, "archive"},
	{0, []byte{0x52, 0x61, 0x72, 0x21}, "archive"},
	{0, []byte{0x37, 0x7a, 0xbc, 0xaf}, "archive"},
}

func detectByExtension(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	lower := strings.ToLower(filepath.Base(name))
	if textExtensions[ext] {
		return "text"
	}
	if imageExtensions[ext] {
		return "image"
	}
	if ext == ".pdf" {
		return "pdf"
	}
	switch ext {
	case ".mp4", ".webm", ".avi", ".mov", ".mkv":
		return "video"
	case ".mp3", ".wav", ".ogg", ".flac", ".m4a":
		return "audio"
	case ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar":
		return "archive"
	}
	if textBasenames[lower] {
		return "text"
	}
	if textBasenames[ext] {
		return "text"
	}
	return ""
}

func detectFileType(name string) string {
	byExt := detectByExtension(name)
	if byExt != "" {
		return byExt
	}
	return "binary"
}

func isTextBuffer(buf []byte) bool {
	if len(buf) == 0 {
		return true
	}
	sample := buf
	if len(sample) > magicByteSample {
		sample = sample[:magicByteSample]
	}
	nulCount := 0
	for _, b := range sample {
		if b == 0 {
			nulCount++
		}
	}
	if float64(nulCount)/float64(len(sample)) > binaryNulRatio {
		return false
	}
	if len(sample) >= 3 && sample[0] == 0xef && sample[1] == 0xbb && sample[2] == 0xbf {
		return true
	}
	controlCount := 0
	for _, b := range sample {
		if b == 0 || (b < 32 && b != 9 && b != 10 && b != 13 && b != 27) {
			controlCount++
		}
	}
	return float64(controlCount)/float64(len(sample)) < 0.10
}

func detectByMagicBytes(buf []byte) string {
	for _, sig := range magicBytes {
		if len(buf) < sig.offset+len(sig.bytes) {
			continue
		}
		match := true
		for i, b := range sig.bytes {
			if buf[sig.offset+i] != b {
				match = false
				break
			}
		}
		if match {
			return sig.ftype
		}
	}
	return ""
}

// hasModeBit returns true when the OS stat file mode contains the given bit.
// Separate from regular-file detection so special files can be classified
// without ever being opened (open on a FIFO would block; devices may hang).
func hasModeBit(mode os.FileMode, bit os.FileMode) bool {
	return mode&bit != 0
}

// detectFileTypeByContent reads the first N bytes and determines if the file
// is text or binary. Non-regular files (FIFOs, devices, sockets) are never
// opened: open(O_RDONLY) on a FIFO with no writer blocks forever.
func detectFileTypeByContent(filePath string) (ftype string, isText bool) {
	byExt := detectByExtension(filePath)
	stat, err := os.Stat(filePath)
	if err != nil {
		if byExt == "" {
			return "binary", false
		}
		return byExt, byExt == "text"
	}
	if !stat.Mode().IsRegular() {
		if isNamedPipe(stat.Mode()) {
			return "fifo", false
		}
		return "special", false
	}
	f, err := os.Open(filePath)
	if err != nil {
		if byExt == "" {
			return "binary", false
		}
		return byExt, byExt == "text"
	}
	defer f.Close()

	buf := make([]byte, magicByteSample)
	n, _ := f.Read(buf)
	sample := buf[:n]

	byMagic := detectByMagicBytes(sample)
	if byMagic != "" {
		return byMagic, false
	}
	if isTextBuffer(sample) {
		return "text", true
	}
	if byExt == "" {
		return "binary", false
	}
	return byExt, false
}

// isNamedPipe reports whether mode corresponds to a FIFO.
func isNamedPipe(mode os.FileMode) bool {
	return hasModeBit(mode, os.ModeNamedPipe)
}

// isSpecialPath reports whether any path component is a special file (fifo,
// socket, device). Used before opening to avoid FIFO/device block.
func isSpecialEntry(mode os.FileMode) bool {
	if mode.IsRegular() {
		return false
	}
	return hasModeBit(mode, os.ModeNamedPipe|os.ModeSocket|os.ModeDevice|os.ModeCharDevice|os.ModeIrregular)
}

func formatFileSize(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	if bytes < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
}

func truncateUTF8(content string, maxBytes int) (string, bool) {
	if len(content) <= maxBytes {
		return content, false
	}
	// Find the largest rune-aligned prefix within maxBytes.
	end := maxBytes
	for end > 0 && !utf8RuneStart(content[end-1]) {
		end--
	}
	if end > 0 {
		// Walk back to a valid rune boundary.
		for end > 0 && (content[end-1]&0xC0) == 0x80 {
			end--
		}
	}
	return content[:end], true
}

func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

func globToRegex(pattern string) *regexp.Regexp {
	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, `\*`, `.*`)
	escaped = strings.ReplaceAll(escaped, `\?`, `.`)
	re, err := regexp.Compile("(?i)^" + escaped + "$")
	if err != nil {
		return regexp.MustCompile("(?i)^" + regexp.QuoteMeta(pattern) + "$")
	}
	return re
}

func shouldExclude(name string, globs []string) bool {
	if len(globs) == 0 {
		return false
	}
	for _, g := range globs {
		if globToRegex(g).MatchString(name) {
			return true
		}
	}
	return false
}

// FileService provides bounded filesystem operations rooted at root.
type FileService struct {
	root       string
	ctxEngine  *ContextEngine
	retrEngine *RetrievalEngine
}

func (s *FileService) engine() *ContextEngine {
	if s.ctxEngine == nil {
		s.ctxEngine = NewContextEngine(s.root)
	}
	return s.ctxEngine
}

func (s *FileService) retrieval() *RetrievalEngine {
	if s.retrEngine == nil {
		s.retrEngine = NewRetrievalEngine(s.root)
	}
	return s.retrEngine
}

func NewFileService(root string) *FileService {
	return &FileService{root: root}
}

func (s *FileService) SetRoot(newRoot string) {
	s.root = newRoot
}

func (s *FileService) atomicWrite(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp-%d-%s", path, os.Getpid(), randomSuffix())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func randomSuffix() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func (s *FileService) wrapErr(input string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("%s. Path not found. Files plugin root is %q. Use paths relative to that root (e.g. \"\" for root, \"Documents\" for a subdirectory).", err.Error(), s.root)
	}
	return err
}

// DirEntry is a directory listing item.
type DirEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"isDir"`
	IsFile    bool   `json:"isFile"`
	IsSymlink bool   `json:"isSymlink"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
	Created   string `json:"created"`
	Type      string `json:"type"`
}

func (s *FileService) ListDir(input string) ([]DirEntry, error) {
	dir, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, s.wrapErr(input, err)
	}

	var items []DirEntry
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		stat := info.Sys()
		_ = stat
		isDir := entry.IsDir()
		items = append(items, DirEntry{
			Name:      entry.Name(),
			Path:      relativePosix(s.root, entryPath, entry.Name()),
			IsDir:     isDir,
			IsFile:    !isDir,
			IsSymlink: entry.Type()&os.ModeSymlink != 0,
			Size:      cond(isDir, 0, info.Size()),
			Modified:  info.ModTime().UTC().Format(time.RFC3339),
			Created:   info.ModTime().UTC().Format(time.RFC3339),
			Type:      cond(isDir, "dir", detectFileType(entry.Name())),
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

// TreeNode is a directory tree node.
type TreeNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"isDir"`
	Size     int64      `json:"size"`
	Modified string     `json:"modified"`
	Type     string     `json:"type"`
	Children []TreeNode `json:"children,omitempty"`
}

func (s *FileService) Tree(input string, depth int, exclude []string, includeFiles bool) ([]TreeNode, error) {
	if depth < 1 {
		depth = 1
	}
	if depth > maxTreeDepth {
		depth = maxTreeDepth
	}
	dir, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, s.wrapErr(input, err)
	}
	return s.buildTree(dir, depth, exclude, includeFiles)
}

func (s *FileService) buildTree(dir string, depth int, exclude []string, includeFiles bool) ([]TreeNode, error) {
	if depth <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var nodes []TreeNode
	for _, entry := range entries {
		if shouldExclude(entry.Name(), exclude) {
			continue
		}
		entryPath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		isDir := entry.IsDir()
		if !includeFiles && !isDir {
			continue
		}
		node := TreeNode{
			Name:     entry.Name(),
			Path:     relativePosix(s.root, entryPath, entry.Name()),
			IsDir:    isDir,
			Size:     cond(isDir, 0, info.Size()),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
			Type:     cond(isDir, "dir", detectFileType(entry.Name())),
		}
		if isDir && depth > 1 {
			children, _ := s.buildTree(entryPath, depth-1, exclude, includeFiles)
			node.Children = children
		}
		nodes = append(nodes, node)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].IsDir != nodes[j].IsDir {
			return nodes[i].IsDir
		}
		return nodes[i].Name < nodes[j].Name
	})
	return nodes, nil
}

// ReadResult is the result of reading a file.
type ReadResult struct {
	Content         string `json:"content"`
	TotalLines      int    `json:"totalLines"`
	TotalBytes      int64  `json:"totalBytes"`
	Truncated       bool   `json:"truncated"`
	TruncatedReason string `json:"truncatedReason,omitempty"`
}

func (s *FileService) ReadFile(input string, opts ReadOpts) (*ReadResult, error) {
	started := time.Now()
	filePath, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, s.wrapErr(input, err)
	}

	ftype, isText := detectFileTypeByContent(filePath)
	if !isText {
		return nil, fmt.Errorf("File is binary (type=%s); read only supports text. Use info to inspect.", ftype)
	}

	totalLines, err := countLines(filePath)
	if err != nil {
		return nil, s.wrapErr(input, err)
	}

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = maxReadBytes
	}

	var selected []string
	var reason string

	switch {
	case opts.Start > 0 && opts.End > 0:
		start := opts.Start
		if start < 1 {
			start = 1
		}
		lines, err := s.readLineRange(filePath, start, opts.End)
		if err != nil {
			return nil, err
		}
		selected = lines
		reason = "startEnd"
	case opts.Head > 0:
		h := opts.Head
		if h > totalLines {
			h = totalLines
		}
		lines, err := s.readLineRange(filePath, 1, h)
		if err != nil {
			return nil, err
		}
		selected = lines
		reason = "head"
	case opts.Tail > 0:
		t := opts.Tail
		if t > totalLines {
			t = totalLines
		}
		lines, err := readTailLines(filePath, t, maxBytes)
		if err != nil {
			return nil, err
		}
		selected = lines
		reason = "tail"
	default:
		raw, err := readPrefix(filePath, maxBytes)
		if err != nil {
			return nil, s.wrapErr(input, err)
		}
		selected = splitLines(string(raw))
		if stat.Size() > int64(maxBytes) {
			reason = "maxBytes"
		}
	}

	stderr("read %s (%d bytes -> %d selected lines, mode=%s) took %s",
		filePath, stat.Size(), len(selected), reason, time.Since(started).Round(time.Millisecond))

	var output string
	if opts.LineNumbers {
		var sb strings.Builder
		for i, line := range selected {
			var lineNo int
			switch reason {
			case "startEnd":
				lineNo = opts.Start + i
			case "tail":
				lineNo = totalLines - len(selected) + i + 1
			default:
				lineNo = i + 1
			}
			fmt.Fprintf(&sb, "%6d|%s", lineNo, line)
			if i < len(selected)-1 {
				sb.WriteString("\n")
			}
		}
		output = sb.String()
	} else {
		output = strings.Join(selected, "\n")
	}

	truncated := reason != ""
	output, byteTruncated := truncateUTF8(output, maxBytes)
	if byteTruncated {
		truncated = true
		reason = "maxBytes"
	}

	return &ReadResult{
		Content:         output,
		TotalLines:      totalLines,
		TotalBytes:      stat.Size(),
		Truncated:       truncated,
		TruncatedReason: reason,
	}, nil
}

// countLines returns the number of lines in a file without loading the whole
// file into memory (chunked newline scan; splitLines-compatible semantics).
// Non-regular files are rejected: opening a FIFO would block forever.
func countLines(filePath string) (int, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	if !stat.Mode().IsRegular() {
		return 0, fmt.Errorf("not a regular file: %s", filePath)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	count := 0
	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			count += bytes.Count(buf[:n], []byte{'\n'})
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	return count + 1, nil
}

// readLineRange returns lines [from..to] (1-based, inclusive) scanning forward
// with bounded memory: it stops as soon as line `to` is read.
func (s *FileService) readLineRange(filePath string, from, to int) ([]string, error) {
	if from < 1 {
		from = 1
	}
	if to < from {
		return []string{}, nil
	}
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filePath)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 16*1024*1024)
	lines := make([]string, 0, to-from+1)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < from {
			continue
		}
		if lineNo > to {
			break
		}
		lines = append(lines, strings.TrimSuffix(scanner.Text(), "\r"))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// readTailLines returns the last n lines. Small files are read fully; large
// files are scanned backwards from the end so only the needed tail is loaded.
func readTailLines(filePath string, n int, capBytes int) ([]string, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filePath)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	const chunkSize = 64 * 1024
	if fi.Size() <= int64(capBytes*2) {
		raw, err := readPrefix(filePath, int(fi.Size()))
		if err != nil {
			return nil, err
		}
		lines := splitLines(string(raw))
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		return lines, nil
	}

	collected := make([]byte, 0, capBytes*2)
	pos := fi.Size()
	for pos > 0 && len(collected) < capBytes*2 {
		sz := int64(chunkSize)
		if sz > pos {
			sz = pos
		}
		pos -= sz
		buf := make([]byte, sz)
		if _, err := f.ReadAt(buf, pos); err != nil && err != io.EOF {
			return nil, err
		}
		collected = append(buf, collected...)
		if bytes.Count(collected, []byte{'\n'}) >= n {
			break
		}
	}
	lines := splitLines(string(collected))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// readPrefix reads at most n bytes from the start of a file (bounded memory).
// Non-regular files are rejected (FIFO/device open would block).
func readPrefix(filePath string, n int) ([]byte, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filePath)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:read], nil
}

// ReadOpts holds optional parameters for ReadFile.
type ReadOpts struct {
	Head        int
	Tail        int
	Start       int
	End         int
	LineNumbers bool
	MaxBytes    int
}

func (s *FileService) WriteFile(input, content string, encoding string) (map[string]any, error) {
	filePath, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return nil, err
	}
	var data []byte
	if encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("invalid base64: %w", err)
		}
		data = decoded
	} else {
		data = []byte(content)
	}
	if err := s.atomicWrite(filePath, data); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":    relativePosix(s.root, filePath, filepath.Base(filePath)),
		"written": true,
	}, nil
}

func (s *FileService) MakeDir(input string) (map[string]any, error) {
	dirPath, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":    relativePosix(s.root, dirPath, filepath.Base(dirPath)),
		"created": true,
	}, nil
}

func (s *FileService) MoveFile(source, destination string) (map[string]any, error) {
	src, err := resolvePath(s.root, source)
	if err != nil {
		return nil, err
	}
	dst, err := resolvePath(s.root, destination)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, s.wrapErr(source, err)
	}
	return map[string]any{
		"from":  relativePosix(s.root, src, filepath.Base(src)),
		"to":    relativePosix(s.root, dst, filepath.Base(dst)),
		"moved": true,
	}, nil
}

func (s *FileService) CopyFile(source, destination string) (map[string]any, error) {
	src, err := resolvePath(s.root, source)
	if err != nil {
		return nil, err
	}
	dst, err := resolvePath(s.root, destination)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(src); err != nil {
		return nil, s.wrapErr(source, err)
	}
	if err := copyRecursive(src, dst); err != nil {
		return nil, err
	}
	return map[string]any{
		"from":   relativePosix(s.root, src, filepath.Base(src)),
		"to":     relativePosix(s.root, dst, filepath.Base(dst)),
		"copied": true,
	}, nil
}

func copyRecursive(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyRecursive(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		// Skip FIFOs/sockets/devices: opening them would block or fail.
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode())
}

func (s *FileService) DeleteFile(input string, recursive bool) (map[string]any, error) {
	target, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(target)
	if err != nil {
		return nil, s.wrapErr(input, err)
	}
	if stat.IsDir() && !recursive {
		entries, err := os.ReadDir(target)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			return nil, fmt.Errorf("Directory is not empty. Use recursive=true to delete.")
		}
	}
	if err := os.RemoveAll(target); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":    relativePosix(s.root, target, filepath.Base(target)),
		"deleted": true,
	}, nil
}

// SearchResult is a single file search match.
type SearchResult struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Type     string `json:"type"`
}

func (s *FileService) SearchFiles(input, pattern string, exclude []string, ftype string, maxDepth int) (map[string]any, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}
	dir, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, s.wrapErr(input, err)
	}
	re := globToRegex(pattern)
	var results []SearchResult
	s.searchRecursive(dir, re, &results, exclude, ftype, maxDepth, 1)
	truncated := len(results) > maxSearchResults
	if len(results) > maxSearchResults {
		results = results[:maxSearchResults]
	}
	return map[string]any{
		"results": results,
		"meta": map[string]any{
			"truncated": truncated,
			"count":     len(results),
			"cap":       maxSearchResults,
		},
	}, nil
}

func (s *FileService) searchRecursive(dir string, re *regexp.Regexp, results *[]SearchResult, exclude []string, ftype string, maxDepth, currentDepth int) {
	if len(*results) >= maxSearchResults || currentDepth > maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if len(*results) >= maxSearchResults {
			return
		}
		if shouldExclude(entry.Name(), exclude) {
			continue
		}
		entryPath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		isDir := entry.IsDir()
		if re.MatchString(entry.Name()) {
			if ftype == "file" && isDir {
				continue
			}
			if ftype == "dir" && !isDir {
				continue
			}
			*results = append(*results, SearchResult{
				Name:     entry.Name(),
				Path:     relativePosix(s.root, entryPath, entry.Name()),
				IsDir:    isDir,
				Size:     cond(isDir, 0, info.Size()),
				Modified: info.ModTime().UTC().Format(time.RFC3339),
				Type:     cond(isDir, "dir", detectFileType(entry.Name())),
			})
		}
		if isDir {
			s.searchRecursive(entryPath, re, results, exclude, ftype, maxDepth, currentDepth+1)
		}
	}
}

// GrepResult is a single grep match.
type GrepResult struct {
	Path    string   `json:"path"`
	Line    int      `json:"line"`
	Content string   `json:"content"`
	Before  []string `json:"before,omitempty"`
	After   []string `json:"after,omitempty"`
}

func (s *FileService) GrepFiles(input, pattern string, opts GrepOpts) (map[string]any, error) {
	started := time.Now()
	target, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(target)
	if err != nil {
		return nil, s.wrapErr(input, err)
	}

	flags := ""
	if opts.IgnoreCase {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	var globRe *regexp.Regexp
	if opts.Glob != "" {
		globRe = globToRegex(opts.Glob)
	}

	cap := opts.MaxResults
	if cap <= 0 || cap > maxSearchResults {
		cap = maxSearchResults
	}

	var results []GrepResult
	if !stat.IsDir() {
		name := filepath.Base(target)
		if globRe == nil || globRe.MatchString(name) {
			s.grepOneFile(target, name, re, &results, opts.Before, opts.After, cap)
		}
	} else {
		s.grepRecursive(target, re, globRe, &results, opts.Before, opts.After, opts.Exclude, cap)
	}

	truncated := len(results) > cap
	if len(results) > cap {
		results = results[:cap]
	}
	stderr("grep %s (input=%q, %d results) took %s", target, input, len(results), time.Since(started).Round(time.Millisecond))
	return map[string]any{
		"results": results,
		"meta": map[string]any{
			"truncated": truncated,
			"count":     len(results),
			"cap":       cap,
		},
	}, nil
}

// GrepOpts holds optional parameters for GrepFiles.
type GrepOpts struct {
	Glob       string
	Before     int
	After      int
	IgnoreCase bool
	Exclude    []string
	MaxResults int
}

func (s *FileService) grepOneFile(entryPath, entryName string, re *regexp.Regexp, results *[]GrepResult, before, after, cap int) {
	if len(*results) >= cap {
		return
	}
	extType := detectFileType(entryName)
	if extType != "text" && extType != "binary" {
		return
	}
	info, err := os.Stat(entryPath)
	if err != nil || info.IsDir() || info.Size() > maxGrepFileBytes {
		return
	}
	// Skip files above the read bound in a cheap, extension-driven way before
	// sniffing content; a 48 MB text blob (e.g. a captured stream) would be
	// read in full otherwise and hold the request for seconds.
	if info.Size() > maxReadBytes && detectByExtension(entryName) != "text" {
		return
	}
	_, isText := detectFileTypeByContent(entryPath)
	if !isText {
		return
	}
	raw, err := os.ReadFile(entryPath)
	if err != nil {
		return
	}
	lines := splitLines(string(raw))
	for i, line := range lines {
		if len(*results) >= cap {
			break
		}
		if re.MatchString(line) {
			content := line
			if len(content) > maxGrepLineLen {
				content = content[:maxGrepLineLen] + "…(truncated)"
			}
			result := GrepResult{
				Path:    relativePosix(s.root, entryPath, entryName),
				Line:    i + 1,
				Content: content,
			}
			if before > 0 {
				start := i - before
				if start < 0 {
					start = 0
				}
				result.Before = lines[start:i]
			}
			if after > 0 {
				end := i + 1 + after
				if end > len(lines) {
					end = len(lines)
				}
				result.After = lines[i+1 : end]
			}
			*results = append(*results, result)
		}
	}
}

func (s *FileService) grepRecursive(dir string, re, globRe *regexp.Regexp, results *[]GrepResult, before, after int, exclude []string, cap int) {
	if len(*results) >= cap {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if len(*results) >= cap {
			return
		}
		if shouldExclude(entry.Name(), exclude) {
			continue
		}
		entryPath := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			s.grepRecursive(entryPath, re, globRe, results, before, after, exclude, cap)
			continue
		}
		if globRe != nil && !globRe.MatchString(entry.Name()) {
			continue
		}
		s.grepOneFile(entryPath, entry.Name(), re, results, before, after, cap)
	}
}

func (s *FileService) FileInfo(input string) (map[string]any, error) {
	filePath, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, s.wrapErr(input, err)
	}
	isDir := stat.IsDir()
	fileType := cond(isDir, "dir", detectFileType(filePath))
	if !isDir {
		ft, _ := detectFileTypeByContent(filePath)
		fileType = ft
	}
	return map[string]any{
		"name":        filepath.Base(filePath),
		"path":        relativePosix(s.root, filePath, filepath.Base(filePath)),
		"isDir":       isDir,
		"isFile":      !isDir,
		"isSymlink":   stat.Mode()&os.ModeSymlink != 0,
		"size":        stat.Size(),
		"modified":    stat.ModTime().UTC().Format(time.RFC3339),
		"created":     stat.ModTime().UTC().Format(time.RFC3339),
		"type":        fileType,
		"permissions": fmt.Sprintf("%o", stat.Mode().Perm()),
	}, nil
}

func (s *FileService) PatchFile(input string, edits []PatchEdit, preview bool) (map[string]any, error) {
	filePath, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, s.wrapErr(input, err)
	}
	if stat.Size() > maxReadBytes {
		return nil, fmt.Errorf("File too large (%s), max %s", formatFileSize(stat.Size()), formatFileSize(maxReadBytes))
	}
	ftype, isText := detectFileTypeByContent(filePath)
	if !isText {
		return nil, fmt.Errorf("File is binary (type=%s); patch only supports text files.", ftype)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	str := string(content)

	occurrences := []int{}
	for _, edit := range edits {
		if !strings.Contains(str, edit.OldString) {
			return nil, fmt.Errorf("old_string not found in file. Ensure the string matches exactly, including whitespace and indentation. (edit %d of %d)", len(occurrences)+1, len(edits))
		}
		count := 0
		if edit.ReplaceAll {
			parts := strings.Split(str, edit.OldString)
			count = len(parts) - 1
			str = strings.Join(parts, edit.NewString)
		} else {
			str = strings.Replace(str, edit.OldString, edit.NewString, 1)
			count = 1
		}
		occurrences = append(occurrences, count)
	}

	if preview {
		return map[string]any{
			"path":        relativePosix(s.root, filePath, filepath.Base(filePath)),
			"patched":     false,
			"applied":     0,
			"occurrences": occurrences,
			"preview":     str,
		}, nil
	}

	if err := s.atomicWrite(filePath, []byte(str)); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":        relativePosix(s.root, filePath, filepath.Base(filePath)),
		"patched":     true,
		"applied":     len(edits),
		"occurrences": occurrences,
	}, nil
}

// PatchEdit is a single string replacement.
type PatchEdit struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (s *FileService) AppendFile(input, content string) (map[string]any, error) {
	filePath, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return nil, err
	}
	existing, _ := os.ReadFile(filePath)
	if err := s.atomicWrite(filePath, append(existing, []byte(content)...)); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":     relativePosix(s.root, filePath, filepath.Base(filePath)),
		"appended": true,
	}, nil
}

func (s *FileService) ExistsFile(input string) (map[string]any, error) {
	filePath, err := resolvePath(s.root, input)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(filePath)
	if err != nil {
		return map[string]any{
			"path":   input,
			"exists": false,
			"isFile": false,
			"isDir":  false,
		}, nil
	}
	return map[string]any{
		"path":   relativePosix(s.root, filePath, filepath.Base(filePath)),
		"exists": true,
		"isFile": !stat.IsDir(),
		"isDir":  stat.IsDir(),
	}, nil
}

func (s *FileService) TouchFile(input string, createParents, updateOnly bool) (map[string]any, error) {
	filePath, resolveErr := resolvePath(s.root, input)
	if resolveErr != nil {
		return nil, resolveErr
	}
	_, err := os.Stat(filePath)
	if err != nil {
		if updateOnly {
			return nil, fmt.Errorf("File does not exist: %s. Use updateOnly=false to create it.", input)
		}
		if createParents {
			if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
				return nil, err
			}
		}
		if err := s.atomicWrite(filePath, []byte{}); err != nil {
			return nil, err
		}
		return map[string]any{
			"path":    relativePosix(s.root, filePath, filepath.Base(filePath)),
			"created": true,
			"touched": false,
		}, nil
	}
	now := time.Now()
	_ = os.Chtimes(filePath, now, now)
	return map[string]any{
		"path":    relativePosix(s.root, filePath, filepath.Base(filePath)),
		"created": false,
		"touched": true,
	}, nil
}

// --- helpers ---

func cond[T any](b bool, t, f T) T {
	if b {
		return t
	}
	return f
}

// Ensure bytes.Buffer is used to avoid unused import.
var _ = bytes.MinRead
var _ = io.EOF
