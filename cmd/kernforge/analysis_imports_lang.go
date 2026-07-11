package main

// Import extraction and resolution for Python, C#, and Rust. Before this the
// dependency graph only populated for Go, C/C++, and JS/TS, so plain
// Python/C#/Rust repositories produced an empty import graph and therefore an
// empty structure-metrics view. These extractors emit normalized, slash- and
// relative-form module tokens that the language-scoped resolver maps back to
// scanned files, keeping edges within the same language family to avoid
// cross-language false positives.

import (
	"path/filepath"
	"strings"
)

var (
	pythonImportExtensions = []string{".py"}
	csharpImportExtensions = []string{".cs"}
	rustImportExtensions   = []string{".rs"}
)

func extractPythonImports(content string) []string {
	out := []string{}
	for _, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "from "):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "from "))
			idx := strings.Index(rest, " import ")
			if idx < 0 {
				continue
			}
			module := strings.TrimSpace(rest[:idx])
			names := strings.TrimSpace(rest[idx+len(" import "):])
			out = append(out, pythonFromImportTokens(module, names)...)
		case strings.HasPrefix(trimmed, "import "):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "import "))
			for _, part := range strings.Split(rest, ",") {
				token := strings.TrimSpace(part)
				if space := strings.Index(token, " as "); space >= 0 {
					token = strings.TrimSpace(token[:space])
				}
				if normalized := pythonModuleToken(token); normalized != "" {
					out = append(out, normalized)
				}
			}
		}
	}
	return analysisUniqueStrings(out)
}

func pythonFromImportTokens(module string, names string) []string {
	module = strings.TrimSpace(module)
	if module == "" {
		return nil
	}
	leadingDots := 0
	for leadingDots < len(module) && module[leadingDots] == '.' {
		leadingDots++
	}
	body := module[leadingDots:]
	if leadingDots == 0 {
		if token := pythonModuleToken(body); token != "" {
			return []string{token}
		}
		return nil
	}
	prefix := "./"
	if leadingDots > 1 {
		prefix = strings.Repeat("../", leadingDots-1)
	}
	if body != "" {
		return []string{prefix + strings.ReplaceAll(body, ".", "/")}
	}
	// "from . import name" / "from .. import name": each name is a submodule.
	tokens := []string{}
	for _, name := range strings.Split(stripImportGroup(names), ",") {
		name = strings.TrimSpace(name)
		if space := strings.Index(name, " as "); space >= 0 {
			name = strings.TrimSpace(name[:space])
		}
		name = strings.TrimSpace(strings.Trim(name, "()"))
		if name == "" || name == "*" {
			continue
		}
		tokens = append(tokens, prefix+name)
	}
	return tokens
}

func pythonModuleToken(module string) string {
	module = strings.TrimSpace(module)
	if module == "" {
		return ""
	}
	return strings.ReplaceAll(module, ".", "/")
}

func stripImportGroup(names string) string {
	names = strings.TrimSpace(names)
	names = strings.TrimPrefix(names, "(")
	names = strings.TrimSuffix(names, ")")
	return names
}

func extractCSharpImports(content string) []string {
	out := []string{}
	for _, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if !strings.HasPrefix(trimmed, "using ") {
			continue
		}
		if !strings.HasSuffix(trimmed, ";") {
			continue
		}
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "using "), ";"))
		body = strings.TrimSpace(strings.TrimPrefix(body, "static "))
		if eq := strings.Index(body, "="); eq >= 0 {
			body = strings.TrimSpace(body[eq+1:])
		}
		if body == "" || strings.ContainsAny(body, " (){}<>") {
			continue
		}
		out = append(out, strings.ReplaceAll(body, ".", "/"))
	}
	return analysisUniqueStrings(out)
}

func extractRustImports(content string) []string {
	out := []string{}
	for _, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "pub ")
		switch {
		case strings.HasPrefix(trimmed, "use "):
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, "use "))
			body = strings.TrimSuffix(body, ";")
			if token := rustUseToken(body); token != "" {
				out = append(out, token)
			}
		case strings.HasPrefix(trimmed, "mod ") && strings.HasSuffix(trimmed, ";"):
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "mod "), ";"))
			if name != "" && !strings.ContainsAny(name, " {}") {
				out = append(out, "./"+name)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func rustUseToken(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if brace := strings.Index(body, "{"); brace >= 0 {
		body = strings.TrimRight(strings.TrimSpace(body[:brace]), ":")
	}
	if as := strings.Index(body, " as "); as >= 0 {
		body = strings.TrimSpace(body[:as])
	}
	prefix := ""
	switch {
	case strings.HasPrefix(body, "crate::"):
		body = strings.TrimPrefix(body, "crate::")
	case strings.HasPrefix(body, "self::"):
		body = strings.TrimPrefix(body, "self::")
		prefix = "./"
	case strings.HasPrefix(body, "super::"):
		body = strings.TrimPrefix(body, "super::")
		prefix = "../"
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return prefix + strings.ReplaceAll(body, "::", "/")
}

// resolveLanguageScopedImport maps a normalized module token to scanned files,
// restricting matches to the given extension set so a Python/C#/Rust import can
// never bind to a file from another language family.
func (a *projectAnalyzer) resolveLanguageScopedImport(snapshot ProjectSnapshot, file ScannedFile, raw string, extensions []string) []string {
	token := filepath.ToSlash(strings.TrimSpace(raw))
	if token == "" {
		return nil
	}
	out := []string{}
	if strings.HasPrefix(token, ".") {
		for _, candidate := range resolveRelativeImport(snapshot, file, token) {
			if structureExtensionInSet(filepath.Ext(candidate), extensions) {
				out = append(out, candidate)
			}
		}
	}
	clean := strings.Trim(token, "/")
	out = append(out, resolvePathWithExtensions(snapshot, clean, extensions)...)
	out = append(out, resolvePathWithExtensions(snapshot, clean+"/mod", extensions)...)
	out = append(out, resolvePathWithExtensions(snapshot, clean+"/__init__", extensions)...)
	base := strings.ToLower(filepath.Base(clean))
	if base != "" && base != "." && base != ".." {
		for path := range snapshot.FilesByPath {
			if !structureExtensionInSet(filepath.Ext(path), extensions) {
				continue
			}
			stem := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
			if stem == base {
				out = append(out, path)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func structureExtensionInSet(ext string, extensions []string) bool {
	ext = strings.ToLower(strings.TrimSpace(ext))
	for _, candidate := range extensions {
		if ext == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}
