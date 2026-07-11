package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ensureImplementationDecisionPathNoLinks(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("implementation decision path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	remainder := strings.TrimPrefix(abs, volume)
	current := volume
	if strings.HasPrefix(remainder, string(filepath.Separator)) {
		current += string(filepath.Separator)
	}
	remainder = strings.TrimLeft(remainder, string(filepath.Separator))
	for _, part := range strings.FieldsFunc(remainder, func(r rune) bool {
		return r == rune(filepath.Separator)
	}) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return nil
			}
			return statErr
		}
		linked, linkErr := implementationDecisionPathEntryIsLink(current, info)
		if linkErr != nil {
			return linkErr
		}
		if linked {
			return fmt.Errorf("implementation decision path contains a symbolic link or reparse point: %s", current)
		}
	}
	return nil
}

func ensureImplementationDecisionRegularOrMissing(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	linked, err := implementationDecisionPathEntryIsLink(path, info)
	if err != nil {
		return err
	}
	if linked || !info.Mode().IsRegular() {
		return fmt.Errorf("implementation decision path is not a regular file: %s", path)
	}
	return nil
}

func ensureImplementationDecisionOpenFileMatchesPath(file *os.File, path string) error {
	if file == nil {
		return os.ErrInvalid
	}
	handleInfo, err := file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	linked, err := implementationDecisionPathEntryIsLink(path, pathInfo)
	if err != nil {
		return err
	}
	if linked || !handleInfo.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || !os.SameFile(handleInfo, pathInfo) {
		return fmt.Errorf("implementation decision open file does not match path: %s", path)
	}
	return nil
}
