//go:build !windows

package main

// platformSystemLocale has no OS-specific source on non-Windows platforms; the
// LANG environment variable handled by getSystemLocale covers POSIX systems.
func platformSystemLocale() string {
	return ""
}
