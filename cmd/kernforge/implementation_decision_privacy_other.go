//go:build !windows

package main

import (
	"os"
	"syscall"
)

func implementationDecisionPathEntryIsLink(_ string, info os.FileInfo) (bool, error) {
	return info.Mode()&os.ModeSymlink != 0, nil
}

func openImplementationDecisionReadFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func ensureImplementationDecisionPrivateDir(path string) error {
	if err := ensureImplementationDecisionPathNoLinks(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := ensureImplementationDecisionPathNoLinks(path); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}
