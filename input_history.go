package main

import "strings"

const maxInputHistoryEntries = 200

type inputHistoryNavigator struct {
	entries []string
	index   int
	draft   string
}

func newInputHistoryNavigator(entries []string, draft string) *inputHistoryNavigator {
	items := append([]string(nil), entries...)
	return &inputHistoryNavigator{
		entries: items,
		index:   len(items),
		draft:   draft,
	}
}

func (n *inputHistoryNavigator) Previous(buffer string) (string, bool) {
	if n == nil || len(n.entries) == 0 {
		return buffer, false
	}
	if n.index == len(n.entries) {
		n.draft = buffer
	}
	if n.index > 0 {
		n.index--
	}
	return n.entries[n.index], true
}

func (n *inputHistoryNavigator) Next(buffer string) (string, bool) {
	if n == nil || len(n.entries) == 0 {
		return buffer, false
	}
	if n.index == len(n.entries) {
		n.draft = buffer
		return buffer, false
	}
	if n.index < len(n.entries)-1 {
		n.index++
		return n.entries[n.index], true
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

func (rt *runtimeState) rememberInputHistory(input string) {
	entry := strings.TrimRight(input, "\r\n")
	if strings.TrimSpace(entry) == "" || strings.Contains(entry, "\n") {
		return
	}
	rt.inputHistory = append(rt.inputHistory, entry)
	if len(rt.inputHistory) > maxInputHistoryEntries {
		rt.inputHistory = append([]string(nil), rt.inputHistory[len(rt.inputHistory)-maxInputHistoryEntries:]...)
	}
}

func (rt *runtimeState) inputHistoryEntries() []string {
	return append([]string(nil), rt.inputHistory...)
}
