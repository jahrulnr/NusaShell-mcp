package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ShellKind enumerates supported shell kinds.
type ShellKind string

const (
	KindAuto       ShellKind = "auto"
	KindBash       ShellKind = "bash"
	KindZsh        ShellKind = "zsh"
	KindPwsh       ShellKind = "pwsh"
	KindPowershell ShellKind = "powershell"
	KindCmd        ShellKind = "cmd"
	KindWsl        ShellKind = "wsl"
	KindUnknown    ShellKind = "unknown"
)

// ShellKinds is the canonical list of supported shell kinds.
var ShellKinds = []ShellKind{
	KindAuto, KindBash, KindZsh, KindPwsh, KindPowershell, KindCmd, KindWsl,
}

// ResolvedShell is a shell resolved from kind or path.
type ResolvedShell struct {
	Kind      ShellKind `json:"kind"`
	Path      string    `json:"path"`
	Available bool      `json:"available"`
	Source    string    `json:"source"`
}

func exeBase(executable string) string {
	normalized := strings.ReplaceAll(executable, "\\", "/")
	base := filepath.Base(normalized)
	base = strings.TrimSuffix(base, ".exe")
	return strings.ToLower(base)
}

func detectShellKind(executable string) ShellKind {
	base := exeBase(executable)
	switch base {
	case "bash":
		return KindBash
	case "zsh":
		return KindZsh
	case "pwsh":
		return KindPwsh
	case "powershell":
		return KindPowershell
	case "cmd":
		return KindCmd
	case "wsl":
		return KindWsl
	default:
		return KindUnknown
	}
}

func execArgsForShell(kind ShellKind, command string) []string {
	switch kind {
	case KindBash, KindZsh:
		return []string{"-lc", command}
	case KindPowershell, KindPwsh:
		return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command}
	case KindCmd:
		return []string{"/d", "/s", "/c", command}
	case KindWsl:
		return []string{"-e", "bash", "-lc", command}
	default:
		if runtime.GOOS == "windows" {
			return []string{"/d", "/s", "/c", command}
		}
		return []string{"-lc", command}
	}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func which(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func firstExisting(candidates []string) string {
	for _, c := range candidates {
		if c != "" && fileExists(c) {
			return c
		}
	}
	return ""
}

func locateNamed(name string, extra []string) (string, string) {
	if w := which(name); w != "" && fileExists(w) {
		return w, "which"
	}
	if d := firstExisting(extra); d != "" {
		return d, "discovery"
	}
	return "", ""
}

func winCandidates(kind ShellKind) []string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	pf86 := os.Getenv("ProgramFiles(x86)")
	if pf86 == "" {
		pf86 = `C:\Program Files (x86)`
	}
	lad := os.Getenv("LOCALAPPDATA")
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = os.Getenv("windir")
	}
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	comSpec := os.Getenv("ComSpec")
	var cands []string
	switch kind {
	case KindPwsh:
		cands = []string{
			filepath.Join(pf, "PowerShell", "7", "pwsh.exe"),
			filepath.Join(pf, "PowerShell", "7-preview", "pwsh.exe"),
		}
	case KindPowershell:
		cands = []string{filepath.Join(sysRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")}
	case KindBash:
		cands = []string{
			filepath.Join(pf, "Git", "bin", "bash.exe"),
			filepath.Join(pf, "Git", "usr", "bin", "bash.exe"),
			filepath.Join(pf86, "Git", "bin", "bash.exe"),
		}
		if lad != "" {
			cands = append(cands, filepath.Join(lad, "Programs", "Git", "bin", "bash.exe"))
		}
	case KindCmd:
		cands = []string{comSpec, filepath.Join(sysRoot, "System32", "cmd.exe")}
	case KindWsl:
		cands = []string{filepath.Join(sysRoot, "System32", "wsl.exe"), "wsl.exe"}
	}
	return cands
}

func resolveCandidatesForKind(kind ShellKind) []string {
	switch kind {
	case KindPwsh:
		return winCandidates(KindPwsh)
	case KindPowershell:
		return winCandidates(KindPowershell)
	case KindBash:
		return winCandidates(KindBash)
	case KindZsh:
		return winCandidates(KindZsh)
	case KindCmd:
		return winCandidates(KindCmd)
	case KindWsl:
		return winCandidates(KindWsl)
	}
	return nil
}

func resolveKind(kind ShellKind) ResolvedShell {
	if runtime.GOOS == "windows" {
		if kind == KindCmd {
			if p := firstExisting(resolveCandidatesForKind(kind)); p != "" {
				return ResolvedShell{Kind: KindCmd, Path: p, Available: true, Source: "discovery"}
			}
			return ResolvedShell{Kind: KindCmd, Path: "cmd.exe", Available: false, Source: "fallback"}
		}
		if kind == KindWsl {
			if p, src := locateNamed("wsl.exe", resolveCandidatesForKind(kind)); p != "" {
				return ResolvedShell{Kind: KindWsl, Path: p, Available: true, Source: src}
			}
			return ResolvedShell{Kind: KindWsl, Path: "wsl.exe", Available: false, Source: "fallback"}
		}
		if kind == KindBash || kind == KindPwsh || kind == KindPowershell || kind == KindZsh {
			names := []string{string(kind)}
			if kind == KindPowershell {
				names = []string{"powershell", "pwsh"}
			}
			if kind == KindPwsh {
				names = []string{"pwsh.exe", "pwsh"}
			}
			if kind == KindBash {
				names = []string{"bash.exe", "bash"}
			}
			cands := resolveCandidatesForKind(kind)
			for _, name := range names {
				if p, src := locateNamed(name, cands); p != "" {
					k := kind
					if kind == KindPowershell && name == "pwsh" {
						k = KindPwsh
					}
					return ResolvedShell{Kind: k, Path: p, Available: true, Source: src}
				}
			}
			return ResolvedShell{Kind: kind, Path: string(kind) + ".exe", Available: false, Source: "fallback"}
		}
	}

	switch kind {
	case KindBash, KindZsh, KindPwsh, KindPowershell:
		names := []string{string(kind)}
		if kind == KindPowershell {
			names = []string{"powershell", "pwsh"}
		}
		for _, name := range names {
			extras := []string{
				"/bin/" + name,
				"/usr/bin/" + name,
				"/usr/local/bin/" + name,
			}
			if p, src := locateNamed(name, extras); p != "" {
				k := kind
				if kind == KindPowershell && name == "pwsh" {
					k = KindPwsh
				}
				return ResolvedShell{Kind: k, Path: p, Available: true, Source: src}
			}
		}
		return ResolvedShell{Kind: kind, Path: string(kind), Available: false, Source: "fallback"}
	case KindCmd, KindWsl:
		return ResolvedShell{Kind: kind, Path: string(kind), Available: false, Source: "fallback"}
	default:
		return ResolvedShell{Kind: KindUnknown, Path: string(kind), Available: false, Source: "fallback"}
	}
}

func resolveAuto() ResolvedShell {
	if runtime.GOOS == "windows" {
		for _, kind := range []ShellKind{KindPwsh, KindPowershell, KindBash, KindCmd} {
			r := resolveKind(kind)
			if r.Available {
				return r
			}
		}
		comSpec := os.Getenv("ComSpec")
		if comSpec == "" {
			comSpec = "cmd.exe"
		}
		return ResolvedShell{Kind: KindCmd, Path: comSpec, Available: true, Source: "fallback"}
	}

	if configured := os.Getenv("SHELL"); configured != "" {
		return ResolvedShell{
			Kind:      detectShellKind(configured),
			Path:      configured,
			Available: true,
			Source:    "env",
		}
	}
	if bash := resolveKind(KindBash); bash.Available {
		return bash
	}
	return ResolvedShell{Kind: KindBash, Path: "/bin/bash", Available: true, Source: "fallback"}
}

// ResolveShell resolves a shell from a kind name or executable path.
func ResolveShell(shell string) ResolvedShell {
	raw := strings.TrimSpace(shell)
	if raw == "" {
		return resolveAuto()
	}
	lower := strings.ToLower(raw)
	if lower == "auto" || lower == "default" {
		return resolveAuto()
	}
	for _, k := range ShellKinds {
		if k != KindAuto && string(k) == lower {
			return resolveKind(k)
		}
	}
	return ResolvedShell{
		Kind:      detectShellKind(raw),
		Path:      raw,
		Available: fileExists(raw),
		Source:    "path",
	}
}

// ListAvailableShells lists installed shells and the auto default.
func ListAvailableShells() map[string]any {
	auto := resolveAuto()
	kinds := []ShellKind{KindBash, KindZsh, KindPwsh, KindPowershell}
	if runtime.GOOS == "windows" {
		kinds = []ShellKind{KindPwsh, KindPowershell, KindBash, KindZsh, KindCmd, KindWsl}
	}

	var shells []ResolvedShell
	seen := map[ShellKind]bool{}
	for _, kind := range kinds {
		r := resolveKind(kind)
		if !r.Available || seen[r.Kind] {
			continue
		}
		seen[r.Kind] = true
		shells = append(shells, r)
	}

	defaultKind := auto.Kind
	if defaultKind == KindUnknown {
		defaultKind = KindAuto
	}

	return map[string]any{
		"platform":    runtime.GOOS,
		"defaultKind": defaultKind,
		"defaultPath": auto.Path,
		"shells":      shells,
	}
}
