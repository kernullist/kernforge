//go:build !windows

package main

func (rt *runtimeState) readInteractiveLine(prompt string, initial string, historyNav *inputHistoryNavigator) (string, bool, error) {
	_ = prompt
	_ = initial
	_ = historyNav
	return "", false, nil
}

func terminalWidth() int {
	return 120
}
