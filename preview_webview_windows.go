//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"

	webview2 "github.com/jchv/go-webview2"
)

func OpenDiffPreviewWebView(preview EditPreview) (bool, error) {
	var (
		mu       sync.Mutex
		decision bool
		decided  bool
		w        webview2.WebView
	)

	w = webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  previewWindowTitle(preview),
			Width:  uint(previewWindowWidth),
			Height: uint(previewWindowHeight),
			Center: true,
		},
	})
	if w == nil {
		return false, fmt.Errorf("failed to initialize WebView2 diff preview")
	}
	defer w.Destroy()

	if err := w.Bind("kfDecision", func(value string) error {
		mu.Lock()
		defer mu.Unlock()

		switch strings.TrimSpace(strings.ToLower(value)) {
		case "apply":
			decision = true
			decided = true
		case "cancel":
			decision = false
			decided = true
		default:
			return fmt.Errorf("invalid preview decision: %s", value)
		}
		go w.Destroy()
		return nil
	}); err != nil {
		return false, err
	}

	w.SetSize(previewWindowWidth, previewWindowHeight, webview2.HintNone)
	w.SetHtml(renderDiffPreviewWebViewHTML(preview))
	w.Run()

	mu.Lock()
	defer mu.Unlock()
	if !decided {
		return false, nil
	}
	return decision, nil
}

func previewWindowTitle(preview EditPreview) string {
	title := strings.TrimSpace(preview.Title)
	if title == "" {
		return "Kernforge Diff Preview"
	}
	return title
}
