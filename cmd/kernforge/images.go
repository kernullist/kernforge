package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type MessageImage struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Detail    string `json:"detail,omitempty"`
}

type EncodedImage struct {
	Path      string
	MediaType string
	Detail    string
	Data      string
}

const (
	imageDetailHigh     = "high"
	imageDetailOriginal = "original"
)

var supportedImageTypes = map[string]string{
	".gif":  "image/gif",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

func parseImageInputList(baseDir, value string) ([]MessageImage, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var images []MessageImage
	for _, raw := range strings.Split(value, ",") {
		item, err := resolveExplicitImageInput(baseDir, raw)
		if err != nil {
			return nil, err
		}
		images = appendUniqueImages(images, item)
	}
	return images, nil
}

func tryResolveMentionImage(baseDir, raw string) (MessageImage, string, bool) {
	item, err := resolveExplicitImageInput(baseDir, raw)
	if err != nil {
		return MessageImage{}, "", false
	}
	absPath := resolveMessageImagePath(baseDir, item.Path)
	return item, relOrAbs(baseDir, absPath), true
}

func resolveExplicitImageInput(baseDir, raw string) (MessageImage, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return MessageImage{}, fmt.Errorf("image path is required")
	}
	path, detail, err := splitImageInputDetail(path)
	if err != nil {
		return MessageImage{}, err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return MessageImage{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return MessageImage{}, err
	}
	if info.IsDir() {
		return MessageImage{}, fmt.Errorf("image path is a directory: %s", raw)
	}
	mediaType, err := detectSupportedImageType(absPath)
	if err != nil {
		return MessageImage{}, err
	}
	return MessageImage{
		Path:      normalizeStoredPromptPath(baseDir, absPath),
		MediaType: mediaType,
		Detail:    detail,
	}, nil
}

func splitImageInputDetail(raw string) (string, string, error) {
	path := strings.TrimSpace(raw)
	detail := ""
	if idx := strings.LastIndex(path, "?detail="); idx >= 0 {
		detail = path[idx+len("?detail="):]
		path = path[:idx]
	}
	if idx := strings.LastIndex(path, "#detail="); idx >= 0 {
		detail = path[idx+len("#detail="):]
		path = path[:idx]
	}
	if strings.TrimSpace(path) == "" {
		return "", "", fmt.Errorf("image path is required")
	}
	normalized, err := normalizeImageDetail(detail)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(path), normalized, nil
}

func normalizeImageDetail(detail string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "":
		return "", nil
	case imageDetailHigh:
		return imageDetailHigh, nil
	case imageDetailOriginal:
		return imageDetailOriginal, nil
	default:
		return "", fmt.Errorf("image detail only supports %q or %q: %s", imageDetailHigh, imageDetailOriginal, detail)
	}
}

func encodedImageDetail(image EncodedImage) string {
	detail := strings.ToLower(strings.TrimSpace(image.Detail))
	if detail == imageDetailOriginal {
		return imageDetailOriginal
	}
	return imageDetailHigh
}

func detectSupportedImageType(path string) (string, error) {
	extType := supportedImageTypes[strings.ToLower(filepath.Ext(path))]
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sniffed := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
	switch {
	case extType != "":
		return extType, nil
	case strings.HasPrefix(sniffed, "image/"):
		switch sniffed {
		case "image/gif", "image/jpeg", "image/png", "image/webp":
			return sniffed, nil
		}
	}
	return "", fmt.Errorf("unsupported image format: %s", path)
}

func encodeMessageImages(baseDir string, images []MessageImage) ([]EncodedImage, error) {
	if len(images) == 0 {
		return nil, nil
	}
	out := make([]EncodedImage, 0, len(images))
	for _, image := range images {
		absPath := resolveMessageImagePath(baseDir, image.Path)
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("read image %s: %w", image.Path, err)
		}
		mediaType := image.MediaType
		if strings.TrimSpace(mediaType) == "" {
			mediaType, err = detectSupportedImageType(absPath)
			if err != nil {
				return nil, err
			}
		}
		detail, err := normalizeImageDetail(image.Detail)
		if err != nil {
			return nil, err
		}
		out = append(out, EncodedImage{
			Path:      image.Path,
			MediaType: mediaType,
			Detail:    detail,
			Data:      base64.StdEncoding.EncodeToString(data),
		})
	}
	return out, nil
}

func imageDataURI(image EncodedImage) string {
	return "data:" + image.MediaType + ";base64," + image.Data
}

func appendUniqueImages(existing []MessageImage, extra ...MessageImage) []MessageImage {
	seen := make(map[string]bool, len(existing))
	for _, item := range existing {
		key := strings.ToLower(strings.TrimSpace(item.Path))
		if key != "" {
			seen[key] = true
		}
	}
	for _, item := range extra {
		key := strings.ToLower(strings.TrimSpace(item.Path))
		if key == "" {
			continue
		}
		if seen[key] {
			for i := range existing {
				existingKey := strings.ToLower(strings.TrimSpace(existing[i].Path))
				if existingKey == key && strings.TrimSpace(existing[i].Detail) == "" && strings.TrimSpace(item.Detail) != "" {
					existing[i].Detail = item.Detail
				}
			}
			continue
		}
		seen[key] = true
		existing = append(existing, item)
	}
	return existing
}

func normalizeStoredPromptPath(baseDir, path string) string {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return filepath.Clean(path)
	}
	targetAbs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return filepath.Clean(targetAbs)
	}
	return filepath.ToSlash(rel)
}

func resolveMessageImagePath(baseDir, storedPath string) string {
	if filepath.IsAbs(storedPath) {
		return filepath.Clean(storedPath)
	}
	return filepath.Clean(filepath.Join(baseDir, storedPath))
}
