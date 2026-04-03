//go:build !windows

package main

import "os"

func replaceFileAtomic(src, dst string) error {
	return os.Rename(src, dst)
}
