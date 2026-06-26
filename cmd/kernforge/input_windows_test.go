//go:build windows

package main

import "testing"

func TestCancelInteractiveLineWithoutWrappedLines(t *testing.T) {
	got := cancelInteractiveLine(0)
	want := "\r\x1b[J"
	if got != want {
		t.Fatalf("cancelInteractiveLine(0) = %q, want %q", got, want)
	}
}

func TestCancelInteractiveLineWithWrappedLines(t *testing.T) {
	got := cancelInteractiveLine(2)
	want := "\x1b[2A\r\x1b[J"
	if got != want {
		t.Fatalf("cancelInteractiveLine(2) = %q, want %q", got, want)
	}
}

func keyDownRune(ch uint16) inputRecord {
	return inputRecord{EventType: keyEventType, KeyEvent: keyEventRecord{KeyDown: 1, UnicodeChar: ch}}
}

func keyUpRune(ch uint16) inputRecord {
	return inputRecord{EventType: keyEventType, KeyEvent: keyEventRecord{KeyDown: 0, UnicodeChar: ch}}
}

func keyDownEnter() inputRecord {
	return inputRecord{EventType: keyEventType, KeyEvent: keyEventRecord{KeyDown: 1, VirtualKeyCode: inputVirtualKeyEnter}}
}

func TestInputRecordsHavePendingKey(t *testing.T) {
	cases := []struct {
		name    string
		records []inputRecord
		want    bool
	}{
		{"empty", nil, false},
		{"printable keydown", []inputRecord{keyDownRune('a')}, true},
		{"keyup only is not pending", []inputRecord{keyUpRune('a')}, false},
		{"enter keydown is pending", []inputRecord{keyDownEnter()}, true},
		{"non-key event ignored", []inputRecord{{EventType: 0x0002 /* mouse */}}, false},
		{"keyup then printable keydown", []inputRecord{keyUpRune('\r'), keyDownRune('d')}, true},
		{"trailing enter keyup is not pending", []inputRecord{keyUpRune('\r')}, false},
	}
	for _, c := range cases {
		if got := inputRecordsHavePendingKey(c.records); got != c.want {
			t.Errorf("%s: inputRecordsHavePendingKey = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEnterInsertsNewline(t *testing.T) {
	cases := []struct {
		name  string
		state uint32
		want  bool
	}{
		{"no modifier submits", 0, false},
		{"left alt inserts newline", leftAltPressed, true},
		{"right alt inserts newline", rightAltPressed, true},
		{"shift alone submits", 0x0010, false},
		{"ctrl alone submits", 0x0008, false},
		{"alt plus shift inserts newline", leftAltPressed | 0x0010, true},
	}
	for _, c := range cases {
		if got := enterInsertsNewline(c.state); got != c.want {
			t.Errorf("%s: enterInsertsNewline(%#x) = %v, want %v", c.name, c.state, got, c.want)
		}
	}
}
