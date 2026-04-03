package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var filePathLocks sync.Map

func lockFilePath(path string) func() {
	key := strings.TrimSpace(path)
	if key == "" {
		return func() {}
	}
	key = filepath.Clean(key)
	actual, _ := filePathLocks.LoadOrStore(key, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	if err := replaceFileAtomic(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
