package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const maxInputHistoryEntries = 200

// inputHistoryFileName is the on-disk store for REPL input history. It lives
// under userConfigDir so history survives across sessions and restarts.
const inputHistoryFileName = "input-history"

type inputHistoryNavigator struct {
	entries []string
	index   int
	draft   string
	// prefix, when non-empty, limits Previous/Next traversal to entries that
	// start with it. This powers prefix search: type a few characters, then
	// press Up to walk only matching history entries.
	prefix string
}

func newInputHistoryNavigator(entries []string, draft string) *inputHistoryNavigator {
	items := append([]string(nil), entries...)
	return &inputHistoryNavigator{
		entries: items,
		index:   len(items),
		draft:   draft,
	}
}

// SetPrefix enables prefix-filtered traversal. Passing an empty prefix clears
// the filter so navigation walks the full history again.
func (n *inputHistoryNavigator) SetPrefix(prefix string) {
	if n == nil {
		return
	}
	n.prefix = prefix
}

func (n *inputHistoryNavigator) matchesPrefix(entry string) bool {
	if n == nil || n.prefix == "" {
		return true
	}
	return strings.HasPrefix(entry, n.prefix)
}

func (n *inputHistoryNavigator) Previous(buffer string) (string, bool) {
	if n == nil || len(n.entries) == 0 {
		return buffer, false
	}
	if n.index == len(n.entries) {
		n.draft = buffer
	}
	// Walk backward to the closest earlier entry that matches the active
	// prefix filter (if any). Without a prefix this is the immediate previous.
	for i := n.index - 1; i >= 0; i-- {
		if n.matchesPrefix(n.entries[i]) {
			n.index = i
			return n.entries[i], true
		}
	}
	return buffer, false
}

func (n *inputHistoryNavigator) Next(buffer string) (string, bool) {
	if n == nil || len(n.entries) == 0 {
		return buffer, false
	}
	if n.index == len(n.entries) {
		n.draft = buffer
		return buffer, false
	}
	// Walk forward to the closest later entry that matches the active prefix
	// filter; if none remain, fall back to the saved draft.
	for i := n.index + 1; i < len(n.entries); i++ {
		if n.matchesPrefix(n.entries[i]) {
			n.index = i
			return n.entries[i], true
		}
	}
	n.index = len(n.entries)
	return n.draft, true
}

func (n *inputHistoryNavigator) SyncBuffer(buffer string) {
	if n == nil {
		return
	}
	if n.index == len(n.entries) {
		n.draft = buffer
		return
	}
	if buffer != n.entries[n.index] {
		n.draft = buffer
		n.index = len(n.entries)
	}
}

// inputHistoryFilePath returns the configured history file path, lazily
// defaulting to userConfigDir when not explicitly set. An empty string is
// returned only when no config dir can be resolved (history stays in-memory).
func (rt *runtimeState) inputHistoryFilePath() string {
	if rt == nil {
		return ""
	}
	if strings.TrimSpace(rt.inputHistoryPath) != "" {
		return rt.inputHistoryPath
	}
	dir := strings.TrimSpace(userConfigDir())
	if dir == "" {
		return ""
	}
	rt.inputHistoryPath = filepath.Join(dir, inputHistoryFileName)
	return rt.inputHistoryPath
}

// loadInputHistory reads persisted history from disk into memory once. It is
// safe to call repeatedly; subsequent calls are no-ops. A missing or unreadable
// file is non-fatal so input still works on a fresh or read-only environment.
func (rt *runtimeState) loadInputHistory() {
	if rt == nil || rt.inputHistoryLoaded {
		return
	}
	rt.inputHistoryLoaded = true
	path := rt.inputHistoryFilePath()
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	var loaded []string
	scanner := bufio.NewScanner(f)
	// Allow long pasted lines without truncation surprises.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		entry := strings.TrimRight(scanner.Text(), "\r\n")
		if strings.TrimSpace(entry) == "" {
			continue
		}
		loaded = append(loaded, entry)
	}
	if len(loaded) > maxInputHistoryEntries {
		loaded = loaded[len(loaded)-maxInputHistoryEntries:]
	}
	// Loaded history seeds the front; any in-memory entries captured before the
	// load (rare) are appended after so chronology is preserved.
	rt.inputHistory = append(loaded, rt.inputHistory...)
}

// persistInputHistory writes the current in-memory history to disk atomically.
// Failures are swallowed: losing persistence must never break the REPL.
func (rt *runtimeState) persistInputHistory() {
	if rt == nil {
		return
	}
	path := rt.inputHistoryFilePath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	var b strings.Builder
	for _, entry := range rt.inputHistory {
		b.WriteString(entry)
		b.WriteByte('\n')
	}
	_ = atomicWriteFile(path, []byte(b.String()), 0o644)
}

func (rt *runtimeState) rememberInputHistory(input string) {
	entry := strings.TrimRight(input, "\r\n")
	if strings.TrimSpace(entry) == "" || strings.Contains(entry, "\n") {
		return
	}
	// Skip consecutive duplicates so repeated commands do not bloat history.
	if n := len(rt.inputHistory); n > 0 && rt.inputHistory[n-1] == entry {
		return
	}
	rt.inputHistory = append(rt.inputHistory, entry)
	if len(rt.inputHistory) > maxInputHistoryEntries {
		rt.inputHistory = append([]string(nil), rt.inputHistory[len(rt.inputHistory)-maxInputHistoryEntries:]...)
	}
	rt.persistInputHistory()
}

func (rt *runtimeState) inputHistoryEntries() []string {
	return append([]string(nil), rt.inputHistory...)
}
