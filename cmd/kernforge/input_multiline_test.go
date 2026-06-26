package main

import (
	"reflect"
	"testing"
)

func runesOf(lines [][]rune) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}

func TestBufferHasNewline(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"hello", false},
		{"a\nb", true},
		{"\n", true},
		{"trailing\n", true},
	}
	for _, c := range cases {
		if got := bufferHasNewline([]rune(c.in)); got != c.want {
			t.Errorf("bufferHasNewline(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSplitRuneLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{""}},
		{"abc", []string{"abc"}},
		{"a\nb", []string{"a", "b"}},
		{"a\n", []string{"a", ""}},
		{"\nb", []string{"", "b"}},
		{"a\nb\nc", []string{"a", "b", "c"}},
		{"\n\n", []string{"", "", ""}},
	}
	for _, c := range cases {
		got := runesOf(splitRuneLines([]rune(c.in)))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitRuneLines(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestWrapRows(t *testing.T) {
	cases := []struct {
		width, termW, want int
	}{
		{0, 80, 1},
		{1, 80, 1},
		{80, 80, 1},  // exactly fills one row (pending-wrap convention)
		{81, 80, 2},  // one past the margin spills to a second row
		{160, 80, 2}, // exactly fills two rows
		{161, 80, 3},
		{10, 0, 1},  // degenerate terminal width
		{10, -5, 1}, // negative width
	}
	for _, c := range cases {
		if got := wrapRows(c.width, c.termW); got != c.want {
			t.Errorf("wrapRows(%d, %d) = %d, want %d", c.width, c.termW, got, c.want)
		}
	}
}

func TestComputeMultilineGeometry(t *testing.T) {
	const firstPrefix = 2 // "> "
	const contPrefix = 4  // "... "
	cases := []struct {
		name      string
		buffer    string
		cursorPos int
		termW     int
		want      multilineGeometry
	}{
		{
			name:      "single line cursor at end",
			buffer:    "hello",
			cursorPos: 5,
			termW:     80,
			want:      multilineGeometry{totalRows: 1, cursorRow: 0, cursorCol: 7},
		},
		{
			name:      "two lines cursor at end",
			buffer:    "ab\ncd",
			cursorPos: 5,
			termW:     80,
			want:      multilineGeometry{totalRows: 2, cursorRow: 1, cursorCol: 6},
		},
		{
			name:      "two lines cursor on first line",
			buffer:    "ab\ncd",
			cursorPos: 1,
			termW:     80,
			want:      multilineGeometry{totalRows: 2, cursorRow: 0, cursorCol: 3},
		},
		{
			name:      "cursor at newline boundary stays on first line",
			buffer:    "ab\ncd",
			cursorPos: 2,
			termW:     80,
			want:      multilineGeometry{totalRows: 2, cursorRow: 0, cursorCol: 4},
		},
		{
			name:      "trailing newline yields empty continuation line",
			buffer:    "ab\n",
			cursorPos: 3,
			termW:     80,
			want:      multilineGeometry{totalRows: 2, cursorRow: 1, cursorCol: 4},
		},
		{
			name:      "line wraps within terminal width",
			buffer:    "0123456789012", // 13 runes
			cursorPos: 13,
			termW:     10,
			want:      multilineGeometry{totalRows: 2, cursorRow: 1, cursorCol: 5},
		},
		{
			name:      "pending wrap at end of exactly full line",
			buffer:    "01234567\nx", // line0 width 2+8 == termW
			cursorPos: 8,
			termW:     10,
			want:      multilineGeometry{totalRows: 2, cursorRow: 0, cursorCol: 9},
		},
		{
			name:      "cjk widths count two cells",
			buffer:    "가나\n다", // each Hangul syllable is 2 cells
			cursorPos: 2,
			termW:     80,
			want:      multilineGeometry{totalRows: 2, cursorRow: 0, cursorCol: 6},
		},
	}
	for _, c := range cases {
		got := computeMultilineGeometry([]rune(c.buffer), c.cursorPos, firstPrefix, contPrefix, c.termW)
		if got != c.want {
			t.Errorf("%s: computeMultilineGeometry(%q, %d) = %+v, want %+v",
				c.name, c.buffer, c.cursorPos, got, c.want)
		}
	}
}

func TestRenderMultilineBody(t *testing.T) {
	cases := []struct {
		buffer string
		want   string
	}{
		{"x", "> x"},
		{"ab\ncd", "> ab\r\n... cd"},
		{"a\n", "> a\r\n... "},
		{"a\nb\nc", "> a\r\n... b\r\n... c"},
	}
	for _, c := range cases {
		got := renderMultilineBody([]rune(c.buffer), "> ", "... ")
		if got != c.want {
			t.Errorf("renderMultilineBody(%q) = %q, want %q", c.buffer, got, c.want)
		}
	}
}

func TestVerticalCursorTarget(t *testing.T) {
	cases := []struct {
		name      string
		buffer    string
		cursorPos int
		dir       int
		want      int
	}{
		{"single line has no vertical target", "abc", 1, -1, -1},
		{"single line down", "abc", 1, 1, -1},
		{"up from first line", "ab\ncd", 1, -1, -1},
		{"down from last line", "ab\ncd", 4, 1, -1},
		{"down preserves column", "abc\ndefg", 1, 1, 5},
		{"up preserves column", "abc\ndefg", 6, -1, 2},
		{"down clamps to shorter line", "abcd\nx", 3, 1, 6},
		{"up clamps to shorter line", "x\nabcd", 5, -1, 1},
	}
	for _, c := range cases {
		got := verticalCursorTarget([]rune(c.buffer), c.cursorPos, c.dir)
		if got != c.want {
			t.Errorf("%s: verticalCursorTarget(%q, %d, %d) = %d, want %d",
				c.name, c.buffer, c.cursorPos, c.dir, got, c.want)
		}
	}
}

func TestLineHomeEndIndex(t *testing.T) {
	cases := []struct {
		name      string
		buffer    string
		cursorPos int
		wantHome  int
		wantEnd   int
	}{
		{"first line", "ab\ncd", 1, 0, 2},
		{"second line", "ab\ncd", 4, 3, 5},
		{"at newline boundary", "ab\ncd", 2, 0, 2},
		{"start of second line", "ab\ncd", 3, 3, 5},
		{"no newline", "hello", 3, 0, 5},
	}
	for _, c := range cases {
		buf := []rune(c.buffer)
		if got := lineHomeIndex(buf, c.cursorPos); got != c.wantHome {
			t.Errorf("%s: lineHomeIndex(%q, %d) = %d, want %d", c.name, c.buffer, c.cursorPos, got, c.wantHome)
		}
		if got := lineEndIndex(buf, c.cursorPos); got != c.wantEnd {
			t.Errorf("%s: lineEndIndex(%q, %d) = %d, want %d", c.name, c.buffer, c.cursorPos, got, c.wantEnd)
		}
	}
}
