//go:build windows

package main

import (
	"fmt"

	webview2 "github.com/jchv/go-webview2"
)

func OpenReadOnlyDiffView(title string, subtitle string, diff string) error {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(previewWindowWidth),
			Height: uint(previewWindowHeight),
			Center: true,
		},
	})
	if w == nil {
		return fmt.Errorf("failed to initialize WebView2 diff view")
	}
	defer w.Destroy()

	if err := w.Bind("kfClose", func() error {
		go w.Destroy()
		return nil
	}); err != nil {
		return err
	}

	w.SetSize(previewWindowWidth, previewWindowHeight, webview2.HintNone)
	w.SetHtml(renderReadOnlyDiffWebViewHTML(title, subtitle, diff))
	w.Run()
	return nil
}
