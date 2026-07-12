package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

func lockFilePathContext(ctx context.Context, path string) (func(), error) {
	key := strings.TrimSpace(path)
	if key == "" {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key = filepath.Clean(key)
	actual, _ := filePathLocks.LoadOrStore(key, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	for {
		if mu.TryLock() {
			return mu.Unlock, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
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
	if err := replaceFileAtomicWithRetry(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// replaceFileAtomicWithRetry renames the already-written+synced temp file over
// the destination, retrying a few times with a short backoff. On Windows an
// antivirus/indexer/editor can briefly hold the destination open, making the
// rename fail transiently with "Access is denied" / "used by another process";
// on any platform a transient lock can do the same. Because the payload is
// already fully durable in the temp file, replaying only the rename is safe and
// idempotent. Before this, a single such blip surfaced as a session Save error
// that hard-aborted the whole turn (discarding completed work at any of the
// ~80 Save checkpoints). Persistent failures (disk full, real permission
// problems) still surface after the bounded retries.
func replaceFileAtomicWithRetry(tmpPath, path string) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err = replaceFileAtomic(tmpPath, path); err == nil {
			return nil
		}
		if attempt < 3 {
			time.Sleep(time.Duration(25*(1<<attempt)) * time.Millisecond)
		}
	}
	return err
}
