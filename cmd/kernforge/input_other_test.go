//go:build !windows

package main

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func newPosixReader(s string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(s))
}

func TestNormalizePastedNewlines(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"abc", "abc"},
		{"a\nb", "a\nb"},
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\r\n\r\nb", "a\n\nb"},
		{"trailing\r\n", "trailing\n"},
	}
	for _, c := range cases {
		if got := normalizePastedNewlines(c.in); got != c.want {
			t.Errorf("normalizePastedNewlines(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReadBracketedPaste(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     string
		wantErr  bool
		leftover string // bytes that should remain in the reader after the marker
	}{
		{name: "simple", in: "hello\x1b[201~", want: "hello"},
		{name: "crlf normalized", in: "a\r\nb\x1b[201~", want: "a\nb"},
		{name: "bare cr normalized", in: "a\rb\x1b[201~", want: "a\nb"},
		{name: "leftover after marker", in: "x\x1b[201~rest", want: "x", leftover: "rest"},
		{name: "empty paste", in: "\x1b[201~", want: ""},
		{name: "truncated without marker", in: "abc", want: "abc", wantErr: true},
	}
	for _, c := range cases {
		reader := newPosixReader(c.in)
		got, err := readBracketedPaste(reader)
		if got != c.want {
			t.Errorf("%s: readBracketedPaste = %q, want %q", c.name, got, c.want)
		}
		if c.wantErr && err == nil {
			t.Errorf("%s: expected an error, got nil", c.name)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if c.leftover != "" {
			rest, _ := io.ReadAll(reader)
			if string(rest) != c.leftover {
				t.Errorf("%s: leftover = %q, want %q", c.name, string(rest), c.leftover)
			}
		}
	}
}

func TestReadPosixKeyPasteAndAltEnter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want posixKeyKind
	}{
		{"paste start", "\x1b[200~", posixKeyPasteStart},
		{"paste end", "\x1b[201~", posixKeyPasteEnd},
		{"alt enter via CR", "\x1b\r", posixKeyAltEnter},
		{"alt enter via LF", "\x1b\n", posixKeyAltEnter},
		{"plain enter", "\r", posixKeyEnter},
		{"delete sequence", "\x1b[3~", posixKeyDelete},
		{"up arrow", "\x1b[A", posixKeyUp},
	}
	for _, c := range cases {
		key, err := readPosixKey(newPosixReader(c.in))
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if key.kind != c.want {
			t.Errorf("%s: readPosixKey(%q).kind = %d, want %d", c.name, c.in, key.kind, c.want)
		}
	}
}

func TestMapPosixTildeSequencePasteMarkers(t *testing.T) {
	if got := mapPosixTildeSequence("200"); got.kind != posixKeyPasteStart {
		t.Errorf("mapPosixTildeSequence(200).kind = %d, want PasteStart", got.kind)
	}
	if got := mapPosixTildeSequence("201"); got.kind != posixKeyPasteEnd {
		t.Errorf("mapPosixTildeSequence(201).kind = %d, want PasteEnd", got.kind)
	}
}
