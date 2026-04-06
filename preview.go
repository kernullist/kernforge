package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func OpenDiffPreview(preview EditPreview) (bool, error) {
	ok, err := OpenDiffPreviewWebView(preview)
	if err == nil {
		return ok, nil
	}

	ok, err = OpenDiffPreviewHTML(preview)
	if err == nil {
		return ok, nil
	}

	previewPath, err := createTempPreviewPath()
	if err != nil {
		return false, err
	}
	resultPath, err := createPreviewResultPath()
	if err != nil {
		_ = os.Remove(previewPath)
		return false, err
	}
	defer os.Remove(previewPath)
	defer os.Remove(resultPath)

	content := preview.Preview
	if strings.TrimSpace(preview.Title) != "" {
		content = preview.Title + "\n\n" + preview.Preview
	}
	if err := os.WriteFile(previewPath, []byte(content), 0o644); err != nil {
		return false, err
	}
	if err := runDiffPreviewProcess(previewPath, resultPath); err != nil {
		return false, err
	}
	return readPreviewDecision(resultPath)
}

func createTempPreviewPath() (string, error) {
	dir := filepath.Join(os.TempDir(), "kernforge-viewer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, "preview-*.txt")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func createPreviewResultPath() (string, error) {
	dir := filepath.Join(os.TempDir(), "kernforge-viewer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, "preview-result-*.txt")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func readPreviewDecision(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(string(data))) {
	case "apply":
		return true, nil
	case "cancel", "":
		return false, nil
	default:
		return false, fmt.Errorf("unknown preview result: %s", strings.TrimSpace(string(data)))
	}
}
