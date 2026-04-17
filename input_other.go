//go:build !windows

package main

func (rt *runtimeState) readInteractiveLine(prompt string, initial string, historyNav *inputHistoryNavigator, allowEmptySubmit bool) (string, bool, error) {
	_ = prompt
	_ = initial
	_ = historyNav
	_ = allowEmptySubmit
	return "", false, nil
}

func terminalWidth() int {
	return 120
}
