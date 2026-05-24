package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type MessageImage struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Detail    string `json:"detail,omitempty"`
}

func messageImageApproxChars(image MessageImage) int {
	return len(image.Path) + len(image.MediaType) + len(image.Detail) + codexResizedImageBytesEstimate
}

type EncodedImage struct {
	Path      string
	MediaType string
	Detail    string
	Data      string
}

const (
	imageDetailHigh         = "high"
	imageDetailOriginal     = "original"
	codexImageCloseTag      = "</image>"
	maxPromptImageDimension = 2048
)

type PromptImage struct {
	Data      []byte
	MediaType string
	Width     int
	Height    int
	Detail    string
}

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

func promptImageDataURI(image PromptImage) string {
	return "data:" + image.MediaType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
}

func loadImageForPrompt(path string, detail string) (PromptImage, error) {
	normalizedDetail, err := normalizeImageDetail(detail)
	if err != nil {
		return PromptImage{}, err
	}
	if normalizedDetail == "" {
		normalizedDetail = imageDetailHigh
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PromptImage{}, fmt.Errorf("unable to read image at `%s`: %w", path, err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return PromptImage{}, fmt.Errorf("unable to process image at `%s`: %w", path, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return PromptImage{}, fmt.Errorf("unable to process image at `%s`: invalid image dimensions", path)
	}
	if normalizedDetail == imageDetailOriginal || (config.Width <= maxPromptImageDimension && config.Height <= maxPromptImageDimension) {
		if canPreservePromptImageSource(format) {
			return PromptImage{
				Data:      data,
				MediaType: promptImageMediaTypeForFormat(format),
				Width:     config.Width,
				Height:    config.Height,
				Detail:    normalizedDetail,
			}, nil
		}
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return PromptImage{}, fmt.Errorf("unable to process image at `%s`: %w", path, err)
		}
		encoded, mediaType, err := encodePromptImage(decoded, "png")
		if err != nil {
			return PromptImage{}, fmt.Errorf("unable to process image at `%s`: %w", path, err)
		}
		return PromptImage{
			Data:      encoded,
			MediaType: mediaType,
			Width:     config.Width,
			Height:    config.Height,
			Detail:    normalizedDetail,
		}, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return PromptImage{}, fmt.Errorf("unable to process image at `%s`: %w", path, err)
	}
	resized := resizePromptImage(decoded, maxPromptImageDimension)
	targetFormat := "png"
	if format == "jpeg" || format == "png" {
		targetFormat = format
	}
	encoded, mediaType, err := encodePromptImage(resized, targetFormat)
	if err != nil {
		return PromptImage{}, fmt.Errorf("unable to process image at `%s`: %w", path, err)
	}
	return PromptImage{
		Data:      encoded,
		MediaType: mediaType,
		Width:     resized.Bounds().Dx(),
		Height:    resized.Bounds().Dy(),
		Detail:    normalizedDetail,
	}, nil
}

func canPreservePromptImageSource(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "png", "jpeg", "webp":
		return true
	default:
		return false
	}
}

func promptImageMediaTypeForFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func resizePromptImage(src image.Image, maxDimension int) image.Image {
	if maxDimension <= 0 {
		maxDimension = maxPromptImageDimension
	}
	bounds := src.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= maxDimension && height <= maxDimension {
		return src
	}
	scale := float64(maxDimension) / float64(width)
	if height > width {
		scale = float64(maxDimension) / float64(height)
	}
	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)
	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, xdraw.Over, nil)
	return dst
}

func encodePromptImage(src image.Image, format string) ([]byte, string, error) {
	var out bytes.Buffer
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg":
		if err := jpeg.Encode(&out, src, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", err
		}
		return out.Bytes(), "image/jpeg", nil
	default:
		if err := png.Encode(&out, src); err != nil {
			return nil, "", err
		}
		return out.Bytes(), "image/png", nil
	}
}

func codexLocalImageOpenTag(index int) string {
	if index < 1 {
		return "<image>"
	}
	return fmt.Sprintf("<image name=[Image #%d]>", index)
}

func appendCodexResponsesImages(content []map[string]any, images []EncodedImage) []map[string]any {
	for i, image := range images {
		content = append(content,
			map[string]any{
				"type": "input_text",
				"text": codexLocalImageOpenTag(i + 1),
			},
			map[string]any{
				"type":      "input_image",
				"image_url": imageDataURI(image),
				"detail":    encodedImageDetail(image),
			},
			map[string]any{
				"type": "input_text",
				"text": codexImageCloseTag,
			},
		)
	}
	return content
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
				if existingKey == key {
					existing[i].Detail = strongestImageDetail(existing[i].Detail, item.Detail)
				}
			}
			continue
		}
		seen[key] = true
		existing = append(existing, item)
	}
	return existing
}

func strongestImageDetail(existing, candidate string) string {
	if imageDetailRank(candidate) > imageDetailRank(existing) {
		return normalizedKnownImageDetail(candidate)
	}
	return existing
}

func imageDetailRank(detail string) int {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case imageDetailOriginal:
		return 2
	case imageDetailHigh:
		return 1
	default:
		return 0
	}
}

func normalizedKnownImageDetail(detail string) string {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case imageDetailOriginal:
		return imageDetailOriginal
	case imageDetailHigh:
		return imageDetailHigh
	default:
		return ""
	}
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
