package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MarkdownMemoryStore is a human-curatable, additive memory layer that lives
// alongside the machine-managed persistent-memory JSON store. Notes are plain
// Markdown files under <baseRoot>/.kernforge/memory/, so an operator can read,
// edit, and version them directly. An index.md file lists the notes for quick
// scanning. This layer never touches the JSON store; the two are independent.
type MarkdownMemoryStore struct {
	Dir string
}

// MarkdownMemoryNote describes a single markdown note discovered in the store.
type MarkdownMemoryNote struct {
	File       string
	Title      string
	ModifiedAt time.Time
	SizeBytes  int64
}

const markdownMemoryIndexFile = "index.md"

var markdownMemoryHeadingPattern = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
var markdownMemorySlugStripPattern = regexp.MustCompile(`[^a-z0-9]+`)

// NewMarkdownMemoryStore builds a store rooted under the given workspace base
// directory. A blank base root yields a nil store so callers degrade to a
// no-op rather than writing to an unexpected location.
func NewMarkdownMemoryStore(baseRoot string) *MarkdownMemoryStore {
	baseRoot = strings.TrimSpace(baseRoot)
	if baseRoot == "" {
		return nil
	}
	return &MarkdownMemoryStore{
		Dir: filepath.Join(baseRoot, userConfigDirName, "memory"),
	}
}

// AppendNote appends a timestamped note to a markdown file. When title is empty
// the note is appended to a shared quick-capture file (notes.md); otherwise it
// goes to a per-title file derived from a slug of the title. The index is
// refreshed afterward. The path of the written file is returned.
func (s *MarkdownMemoryStore) AppendNote(title, body string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("markdown memory store is not configured for this workspace")
	}
	body = strings.TrimRight(body, "\r\n \t")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("note body is empty")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	title = strings.TrimSpace(title)
	fileName := "notes.md"
	if title != "" {
		if slug := markdownMemorySlug(title); slug != "" {
			fileName = slug + ".md"
		}
	}
	path := filepath.Join(s.Dir, fileName)

	unlock := lockFilePath(path)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		unlock()
		return "", err
	}
	var b strings.Builder
	if len(existing) == 0 {
		heading := title
		if heading == "" {
			heading = "Notes"
		}
		b.WriteString("# " + heading + "\n\n")
	} else {
		b.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	// Each capture is a dated section so the file stays readable and append-only.
	b.WriteString("## " + time.Now().Format("2006-01-02 15:04:05") + "\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	if writeErr := atomicWriteFile(path, []byte(b.String()), 0o644); writeErr != nil {
		unlock()
		return "", writeErr
	}
	unlock()
	// Index rebuild is best-effort; a stale index must not fail a capture.
	_ = s.RebuildIndex()
	return path, nil
}

// ListNotes returns the markdown notes in the store, most-recently-modified
// first. The index file itself is excluded from the listing.
func (s *MarkdownMemoryStore) ListNotes() ([]MarkdownMemoryNote, error) {
	if s == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var notes []MarkdownMemoryNote
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), ".md") || strings.EqualFold(name, markdownMemoryIndexFile) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		path := filepath.Join(s.Dir, name)
		notes = append(notes, MarkdownMemoryNote{
			File:       name,
			Title:      markdownMemoryTitle(path, name),
			ModifiedAt: info.ModTime(),
			SizeBytes:  info.Size(),
		})
	}
	sort.Slice(notes, func(i, j int) bool {
		if !notes[i].ModifiedAt.Equal(notes[j].ModifiedAt) {
			return notes[i].ModifiedAt.After(notes[j].ModifiedAt)
		}
		return notes[i].File < notes[j].File
	})
	return notes, nil
}

// ReadNote returns the raw contents of a note by file name (with or without the
// .md extension).
func (s *MarkdownMemoryStore) ReadNote(file string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("markdown memory store is not configured for this workspace")
	}
	file = strings.TrimSpace(file)
	if file == "" {
		return "", fmt.Errorf("note file name is required")
	}
	// Guard against path traversal; only allow a bare file inside the store.
	if strings.ContainsAny(file, `/\`) || file == "." || file == ".." {
		return "", fmt.Errorf("invalid note name %q", file)
	}
	if !strings.EqualFold(filepath.Ext(file), ".md") {
		file += ".md"
	}
	path := filepath.Join(s.Dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RebuildIndex regenerates index.md from the current set of notes. It is safe
// to call when the store directory does not yet exist (it is created on demand
// only when there is at least one note to index).
func (s *MarkdownMemoryStore) RebuildIndex() error {
	if s == nil {
		return nil
	}
	notes, err := s.ListNotes()
	if err != nil {
		return err
	}
	if len(notes) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("# Memory Index\n\n")
	b.WriteString("Human-curatable workspace memory. Edit any note file directly.\n\n")
	for _, note := range notes {
		title := note.Title
		if strings.TrimSpace(title) == "" {
			title = note.File
		}
		fmt.Fprintf(&b, "- [%s](%s) - updated %s\n", title, note.File, note.ModifiedAt.Format("2006-01-02 15:04"))
	}
	path := filepath.Join(s.Dir, markdownMemoryIndexFile)
	return atomicWriteFile(path, []byte(b.String()), 0o644)
}

// markdownMemorySlug converts a title into a filesystem-safe slug.
func markdownMemorySlug(title string) string {
	slug := markdownMemorySlugStripPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 64 {
		slug = strings.Trim(slug[:64], "-")
	}
	return slug
}

// markdownMemoryTitle extracts the first level-1 heading from a note, falling
// back to the file name (without extension) when no heading is present.
func markdownMemoryTitle(path, file string) string {
	data, err := os.ReadFile(path)
	if err == nil {
		if match := markdownMemoryHeadingPattern.FindStringSubmatch(string(data)); len(match) == 2 {
			if title := strings.TrimSpace(match[1]); title != "" {
				return title
			}
		}
	}
	return strings.TrimSuffix(file, filepath.Ext(file))
}
