package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type diffPreviewLine struct {
	Class   string
	Marker  string
	LineNo  string
	Content string
}

func OpenDiffPreviewHTML(preview EditPreview) (bool, error) {
	token, err := randomPreviewToken()
	if err != nil {
		return false, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false, err
	}
	defer listener.Close()

	decisionCh := make(chan bool, 1)
	doneCh := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, renderDiffPreviewHTML(preview, token))
	})
	mux.HandleFunc("/decision", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		decision := strings.TrimSpace(strings.ToLower(r.FormValue("decision")))
		switch decision {
		case "apply":
			select {
			case decisionCh <- true:
			default:
			}
		case "cancel":
			select {
			case decisionCh <- false:
			default:
			}
		default:
			http.Error(w, "invalid decision", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, renderDiffPreviewDecisionHTML(decision == "apply"))
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		defer close(doneCh)
		_ = server.Serve(listener)
	}()

	url := fmt.Sprintf("http://%s/?token=%s", listener.Addr().String(), token)
	if err := OpenExternalURL(url); err != nil {
		_ = server.Shutdown(context.Background())
		<-doneCh
		return false, err
	}

	decision := <-decisionCh
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	<-doneCh
	return decision, nil
}

func randomPreviewToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func renderDiffPreviewHTML(preview EditPreview, token string) string {
	title := strings.TrimSpace(preview.Title)
	if title == "" {
		title = "Kernforge Diff Preview"
	}
	lines := parseDiffPreviewLines(preview.Preview)
	metrics := summarizeDiffPreview(lines)
	var rows []string
	for _, line := range lines {
		rows = append(rows, fmt.Sprintf(
			`<div class="diff-row %s"><div class="diff-gutter">%s</div><div class="diff-line">%s</div><div class="diff-code">%s</div></div>`,
			line.Class,
			htmlEscape(valueOrDefault(line.Marker, " ")),
			htmlEscape(line.LineNo),
			htmlEscape(line.Content),
		))
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&family=IBM+Plex+Mono:wght@400;500&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #07111d;
      --bg-2: #0c1728;
      --surface: rgba(10, 18, 32, 0.82);
      --surface-2: rgba(12, 23, 40, 0.92);
      --border: rgba(148, 163, 184, 0.16);
      --text: #e7eefb;
      --text-dim: #93a7c4;
      --accent: #7dd3fc;
      --accent-2: #f59e0b;
      --add: #34d399;
      --remove: #fb7185;
      --context: #cbd5e1;
      --meta: #fbbf24;
      --shadow: 0 24px 60px rgba(0, 0, 0, 0.35);
    }
    * {
      box-sizing: border-box;
    }
    html, body {
      margin: 0;
      min-height: 100%%;
      background:
        radial-gradient(circle at top right, rgba(125, 211, 252, 0.18), transparent 24%%),
        radial-gradient(circle at left center, rgba(245, 158, 11, 0.12), transparent 18%%),
        linear-gradient(180deg, var(--bg), var(--bg-2));
      color: var(--text);
      font-family: "Space Grotesk", system-ui, sans-serif;
    }
    body {
      padding: 28px 18px 120px;
    }
    .shell {
      max-width: 1320px;
      margin: 0 auto;
      display: grid;
      gap: 18px;
    }
    .hero,
    .panel,
    .toolbar {
      border: 1px solid var(--border);
      background: var(--surface);
      backdrop-filter: blur(18px);
      box-shadow: var(--shadow);
    }
    .hero {
      border-radius: 28px;
      padding: 28px;
      display: grid;
      gap: 18px;
    }
    .eyebrow {
      font: 500 12px/1 "IBM Plex Mono", monospace;
      text-transform: uppercase;
      letter-spacing: 0.16em;
      color: var(--accent);
    }
    .hero-top {
      display: flex;
      justify-content: space-between;
      gap: 18px;
      align-items: start;
    }
    h1 {
      margin: 0;
      font-size: clamp(32px, 5vw, 56px);
      line-height: 0.96;
    }
    .subtitle {
      max-width: 780px;
      color: var(--text-dim);
      font-size: 15px;
      line-height: 1.7;
    }
    .pill-row {
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
    }
    .pill {
      min-width: 140px;
      padding: 14px 16px;
      border-radius: 18px;
      background: rgba(15, 23, 42, 0.72);
      border: 1px solid rgba(148, 163, 184, 0.14);
    }
    .pill-label {
      font: 500 11px/1 "IBM Plex Mono", monospace;
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: 0.12em;
    }
    .pill-value {
      margin-top: 10px;
      font-size: 28px;
      font-weight: 700;
    }
    .pill-value.add {
      color: var(--add);
    }
    .pill-value.remove {
      color: var(--remove);
    }
    .pill-value.context {
      color: var(--context);
    }
    .panel {
      border-radius: 24px;
      overflow: hidden;
    }
    .panel-header {
      display: flex;
      justify-content: space-between;
      gap: 18px;
      padding: 18px 22px;
      background: rgba(15, 23, 42, 0.74);
      border-bottom: 1px solid var(--border);
    }
    .panel-title {
      font-size: 20px;
      font-weight: 700;
    }
    .panel-meta {
      color: var(--text-dim);
      font: 400 12px/1.6 "IBM Plex Mono", monospace;
    }
    .diff-table {
      width: 100%%;
      overflow: auto;
      background:
        linear-gradient(180deg, rgba(7, 17, 29, 0.92), rgba(9, 20, 35, 0.96));
    }
    .diff-row {
      display: grid;
      grid-template-columns: 56px 86px minmax(0, 1fr);
      align-items: start;
      border-bottom: 1px solid rgba(148, 163, 184, 0.08);
      font: 400 13px/1.65 "IBM Plex Mono", monospace;
    }
    .diff-row:last-child {
      border-bottom: none;
    }
    .diff-gutter,
    .diff-line,
    .diff-code {
      padding: 10px 14px;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .diff-gutter,
    .diff-line {
      color: var(--text-dim);
      user-select: none;
    }
    .diff-code {
      color: var(--text);
    }
    .diff-row.diff-add {
      background: linear-gradient(90deg, rgba(52, 211, 153, 0.12), rgba(52, 211, 153, 0.03));
    }
    .diff-row.diff-add .diff-gutter,
    .diff-row.diff-add .diff-code {
      color: #d3fff0;
    }
    .diff-row.diff-remove {
      background: linear-gradient(90deg, rgba(251, 113, 133, 0.12), rgba(251, 113, 133, 0.03));
    }
    .diff-row.diff-remove .diff-gutter,
    .diff-row.diff-remove .diff-code {
      color: #ffd7df;
    }
    .diff-row.diff-meta {
      background: linear-gradient(90deg, rgba(245, 158, 11, 0.12), rgba(245, 158, 11, 0.03));
    }
    .diff-row.diff-meta .diff-code {
      color: #fde7b0;
      font-weight: 500;
    }
    .diff-row.diff-title {
      background: rgba(125, 211, 252, 0.08);
    }
    .diff-row.diff-title .diff-code {
      color: #dff5ff;
      font-weight: 600;
    }
    .toolbar {
      position: fixed;
      left: 18px;
      right: 18px;
      bottom: 18px;
      border-radius: 22px;
      padding: 14px 18px;
      display: flex;
      justify-content: space-between;
      gap: 16px;
      align-items: center;
      max-width: 1320px;
      margin: 0 auto;
    }
    .toolbar-wrap {
      position: sticky;
      bottom: 0;
    }
    .toolbar-copy {
      color: var(--text-dim);
      font-size: 14px;
      line-height: 1.6;
    }
    .toolbar-copy strong {
      color: var(--text);
    }
    .actions {
      display: flex;
      gap: 12px;
      flex-wrap: wrap;
    }
    button {
      appearance: none;
      border: none;
      border-radius: 14px;
      padding: 14px 18px;
      font: 600 14px/1 "Space Grotesk", system-ui, sans-serif;
      cursor: pointer;
      transition: transform 120ms ease, opacity 120ms ease, box-shadow 120ms ease;
    }
    button:hover {
      transform: translateY(-1px);
    }
    button:active {
      transform: translateY(0);
    }
    .approve {
      color: #04140f;
      background: linear-gradient(135deg, #34d399, #7dd3fc);
      box-shadow: 0 10px 24px rgba(52, 211, 153, 0.24);
    }
    .cancel {
      color: var(--text);
      background: rgba(15, 23, 42, 0.92);
      border: 1px solid rgba(148, 163, 184, 0.18);
    }
    .shortcut {
      color: var(--text-dim);
      font: 400 12px/1.6 "IBM Plex Mono", monospace;
    }
    @media (max-width: 900px) {
      body {
        padding: 18px 12px 144px;
      }
      .toolbar {
        left: 12px;
        right: 12px;
        bottom: 12px;
        flex-direction: column;
        align-items: stretch;
      }
      .hero-top,
      .panel-header {
        flex-direction: column;
      }
      .diff-row {
        grid-template-columns: 42px 68px minmax(0, 1fr);
      }
    }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <div class="eyebrow">Kernforge Review Surface</div>
      <div class="hero-top">
        <div>
          <h1>%s</h1>
          <div class="subtitle">Review the proposed patch in a browser-grade diff surface before allowing the write. Added, removed, and unchanged lines are visually separated so you can scan deltas faster.</div>
        </div>
        <div class="shortcut">Shortcuts: <strong>A</strong> apply, <strong>Escape</strong> cancel</div>
      </div>
      <div class="pill-row">
        <div class="pill"><div class="pill-label">Added</div><div class="pill-value add">%d</div></div>
        <div class="pill"><div class="pill-label">Removed</div><div class="pill-value remove">%d</div></div>
        <div class="pill"><div class="pill-label">Context</div><div class="pill-value context">%d</div></div>
        <div class="pill"><div class="pill-label">Visible lines</div><div class="pill-value">%d</div></div>
      </div>
    </section>

    <section class="panel">
      <div class="panel-header">
        <div class="panel-title">Diff Review</div>
        <div class="panel-meta">Local preview session. Actions are sent only to this temporary loopback server.</div>
      </div>
      <div class="diff-table">%s</div>
    </section>
  </div>

  <div class="toolbar-wrap">
    <div class="toolbar">
      <div class="toolbar-copy"><strong>Approve</strong> to apply the patch, or <strong>Cancel</strong> to leave the workspace unchanged.</div>
      <div class="actions">
        <button class="cancel" type="button" onclick="submitDecision('cancel')">Cancel</button>
        <button class="approve" type="button" onclick="submitDecision('apply')">Apply Patch</button>
      </div>
    </div>
  </div>

  <script>
    let submitted = false;
    async function submitDecision(decision)
    {
      if (submitted)
      {
        return;
      }
      submitted = true;
      const body = new URLSearchParams();
      body.set('decision', decision);
      await fetch('/decision?token=%s', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8' },
        body: body.toString()
      });
      document.body.innerHTML = '<div style="min-height:100vh;display:grid;place-items:center;padding:24px;font-family:Space Grotesk,system-ui,sans-serif;background:linear-gradient(180deg,#07111d,#0c1728);color:#e7eefb"><div style="max-width:560px;padding:28px;border-radius:24px;border:1px solid rgba(148,163,184,0.16);background:rgba(10,18,32,0.84);box-shadow:0 24px 60px rgba(0,0,0,0.35)"><div style="font:500 12px/1 IBM Plex Mono,monospace;letter-spacing:0.16em;text-transform:uppercase;color:#7dd3fc">Kernforge Review Surface</div><h1 style="margin:16px 0 12px;font-size:32px;line-height:1">Decision recorded</h1><p style="margin:0;color:#93a7c4;line-height:1.7">The preview has been closed on the Kernforge side. You can close this tab.</p></div></div>';
    }
    window.addEventListener('keydown', function (event)
    {
      if (event.key === 'Escape')
      {
        event.preventDefault();
        submitDecision('cancel');
      }
      if ((event.key === 'a' || event.key === 'A') && !event.ctrlKey && !event.metaKey && !event.altKey)
      {
        event.preventDefault();
        submitDecision('apply');
      }
    });
    window.addEventListener('pagehide', function ()
    {
      if (submitted)
      {
        return;
      }
      const body = new URLSearchParams();
      body.set('decision', 'cancel');
      navigator.sendBeacon('/decision?token=%s', body);
    });
  </script>
</body>
</html>`,
		htmlEscape(title),
		htmlEscape(title),
		metrics.Added,
		metrics.Removed,
		metrics.Context,
		len(lines),
		joinOrFallback(rows, `<div class="diff-row diff-meta"><div class="diff-gutter"></div><div class="diff-line"></div><div class="diff-code">No diff content available.</div></div>`),
		token,
		token,
	)
}

func renderDiffPreviewDecisionHTML(applied bool) string {
	headline := "Review canceled"
	body := "The proposed patch was not applied."
	if applied {
		headline = "Patch approved"
		body = "The proposed patch has been approved and Kernforge can continue."
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <style>
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      padding: 24px;
      background: linear-gradient(180deg, #07111d, #0c1728);
      color: #e7eefb;
      font-family: "Segoe UI", sans-serif;
    }
    .card {
      max-width: 560px;
      padding: 28px;
      border-radius: 24px;
      border: 1px solid rgba(148, 163, 184, 0.16);
      background: rgba(10, 18, 32, 0.84);
      box-shadow: 0 24px 60px rgba(0, 0, 0, 0.35);
    }
    .eyebrow {
      color: #7dd3fc;
      font: 500 12px/1 Consolas, monospace;
      letter-spacing: 0.14em;
      text-transform: uppercase;
    }
    h1 {
      margin: 16px 0 12px;
      font-size: 34px;
      line-height: 1;
    }
    p {
      margin: 0;
      color: #93a7c4;
      line-height: 1.7;
    }
  </style>
</head>
<body>
  <div class="card">
    <div class="eyebrow">Kernforge Review Surface</div>
    <h1>%s</h1>
    <p>%s You can close this tab.</p>
  </div>
</body>
</html>`,
		htmlEscape(headline),
		htmlEscape(headline),
		htmlEscape(body),
	)
}

func parseDiffPreviewLines(preview string) []diffPreviewLine {
	rawLines := strings.Split(strings.ReplaceAll(preview, "\r\n", "\n"), "\n")
	lines := make([]diffPreviewLine, 0, len(rawLines))
	for _, raw := range rawLines {
		lines = append(lines, classifyDiffPreviewLine(raw))
	}
	return lines
}

func classifyDiffPreviewLine(line string) diffPreviewLine {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return diffPreviewLine{
			Class:   "diff-context",
			Marker:  "",
			LineNo:  "",
			Content: "",
		}
	}
	if strings.HasPrefix(trimmed, "Preview for ") || strings.HasPrefix(trimmed, "Selection-focused preview") || strings.HasPrefix(trimmed, "Selection focus for ") {
		return diffPreviewLine{
			Class:   "diff-title",
			Marker:  "",
			LineNo:  "",
			Content: trimmed,
		}
	}
	if strings.HasPrefix(trimmed, "--- ") || strings.HasPrefix(trimmed, "+++ ") {
		return diffPreviewLine{
			Class:   "diff-meta",
			Marker:  "",
			LineNo:  "",
			Content: trimmed,
		}
	}

	marker := line[:1]
	if marker == " " || marker == "+" || marker == "-" {
		rest := line[1:]
		parts := strings.SplitN(rest, "|", 2)
		if len(parts) == 2 {
			lineNo := strings.TrimSpace(parts[0])
			if _, err := strconv.Atoi(lineNo); err == nil {
				className := "diff-context"
				switch marker {
				case "+":
					className = "diff-add"
				case "-":
					className = "diff-remove"
				}
				return diffPreviewLine{
					Class:   className,
					Marker:  marker,
					LineNo:  lineNo,
					Content: strings.TrimLeft(parts[1], " "),
				}
			}
		}
	}

	return diffPreviewLine{
		Class:   "diff-meta",
		Marker:  "",
		LineNo:  "",
		Content: trimmed,
	}
}

type diffPreviewMetrics struct {
	Added   int
	Removed int
	Context int
}

func summarizeDiffPreview(lines []diffPreviewLine) diffPreviewMetrics {
	metrics := diffPreviewMetrics{}
	for _, line := range lines {
		switch line.Class {
		case "diff-add":
			metrics.Added++
		case "diff-remove":
			metrics.Removed++
		case "diff-context":
			metrics.Context++
		}
	}
	return metrics
}

func renderDiffPreviewWebViewHTML(preview EditPreview) string {
	return renderDiffPreviewSurfaceHTML(preview, diffPreviewSurfaceOptions{
		ActionScript: `
    let submitted = false;
    function resolveDecisionBridge()
    {
      if (typeof kfDecision === 'function')
      {
        return kfDecision;
      }
      if (typeof window !== 'undefined' && typeof window.kfDecision === 'function')
      {
        return window.kfDecision;
      }
      return null;
    }
    async function submitDecision(decision)
    {
      if (submitted)
      {
        return;
      }
      submitted = true;
      try
      {
        const bridge = resolveDecisionBridge();
        if (!bridge)
        {
          throw new Error('kfDecision bridge is unavailable');
        }
        await bridge(decision);
        if (typeof window !== 'undefined' && typeof window.close === 'function')
        {
          setTimeout(function () { window.close(); }, 30);
        }
      }
      catch (error)
      {
        console.error(error);
        submitted = false;
      }
      document.body.innerHTML = '<div style="min-height:100vh;display:grid;place-items:center;padding:24px;font-family:Space Grotesk,system-ui,sans-serif;background:linear-gradient(180deg,#07111d,#0c1728);color:#e7eefb"><div style="max-width:560px;padding:28px;border-radius:24px;border:1px solid rgba(148,163,184,0.16);background:rgba(10,18,32,0.84);box-shadow:0 24px 60px rgba(0,0,0,0.35)"><div style="font:500 12px/1 IBM Plex Mono,monospace;letter-spacing:0.16em;text-transform:uppercase;color:#7dd3fc">Kernforge Review Surface</div><h1 style="margin:16px 0 12px;font-size:32px;line-height:1">Decision recorded</h1><p style="margin:0;color:#93a7c4;line-height:1.7">The preview has been closed on the Kernforge side.</p></div></div>';
    }
    window.addEventListener('keydown', function (event)
    {
      if (event.key === 'Escape')
      {
        event.preventDefault();
        submitDecision('cancel');
      }
      if ((event.key === 'a' || event.key === 'A') && !event.ctrlKey && !event.metaKey && !event.altKey)
      {
        event.preventDefault();
        submitDecision('apply');
      }
    });
`,
	})
}

func renderReadOnlyDiffWebViewHTML(title string, subtitle string, diff string) string {
	preview := EditPreview{
		Title:   title,
		Preview: diff,
	}
	if strings.TrimSpace(subtitle) == "" {
		subtitle = "Read-only git diff view."
	}
	return renderDiffPreviewSurfaceHTML(preview, diffPreviewSurfaceOptions{
		Subtitle:          subtitle,
		ToolbarCopy:       "Read-only diff view. Close the window when you are done reviewing.",
		ActionHTML:        ``,
		StructuredUnified: true,
		ShortcutLabel:     "Use the window close button to dismiss this view",
		HideActions:       true,
		ActionScript:      ``,
	})
}

type diffPreviewSurfaceOptions struct {
	Subtitle          string
	ToolbarCopy       string
	ActionHTML        string
	ActionScript      string
	StructuredUnified bool
	ShortcutLabel     string
	HideActions       bool
}

func renderDiffPreviewSurfaceHTML(preview EditPreview, opts diffPreviewSurfaceOptions) string {
	title := strings.TrimSpace(preview.Title)
	if title == "" {
		title = "Kernforge Diff Preview"
	}
	subtitle := strings.TrimSpace(opts.Subtitle)
	if subtitle == "" {
		subtitle = "Review the proposed patch in a browser-grade diff surface before allowing the write. Added, removed, and unchanged lines are visually separated so you can scan deltas faster."
	}
	toolbarCopy := strings.TrimSpace(opts.ToolbarCopy)
	if toolbarCopy == "" {
		toolbarCopy = "<strong>Approve</strong> to apply the patch, or <strong>Cancel</strong> to leave the workspace unchanged."
	}
	actionHTML := strings.TrimSpace(opts.ActionHTML)
	if actionHTML == "" && !opts.HideActions {
		actionHTML = `
        <button class="cancel" type="button" onclick="submitDecision('cancel')">Cancel</button>
        <button class="approve" type="button" onclick="submitDecision('apply')">Apply Patch</button>`
	}
	shortcutLabel := strings.TrimSpace(opts.ShortcutLabel)
	if shortcutLabel == "" {
		shortcutLabel = "Shortcuts: <strong>A</strong> apply, <strong>Escape</strong> cancel"
	}
	lines := parseDiffPreviewLines(preview.Preview)
	metrics := summarizeDiffPreview(lines)
	contentClass := "diff-table"
	contentHTML := ""
	if opts.StructuredUnified {
		if rendered, structuredMetrics, ok := renderUnifiedDiffModesHTML(preview.Preview); ok {
			contentClass = "diff-files"
			contentHTML = rendered
			metrics = structuredMetrics
		}
	}
	if contentHTML == "" {
		var rows []string
		for _, line := range lines {
			rows = append(rows, fmt.Sprintf(
				`<div class="diff-row %s"><div class="diff-gutter">%s</div><div class="diff-line">%s</div><div class="diff-code">%s</div></div>`,
				line.Class,
				htmlEscape(valueOrDefault(line.Marker, " ")),
				htmlEscape(line.LineNo),
				htmlEscape(line.Content),
			))
		}
		contentHTML = joinOrFallback(rows, `<div class="diff-row diff-meta"><div class="diff-gutter"></div><div class="diff-line"></div><div class="diff-code">No diff content available.</div></div>`)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&family=IBM+Plex+Mono:wght@400;500&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #07111d;
      --bg-2: #0c1728;
      --surface: rgba(10, 18, 32, 0.82);
      --surface-2: rgba(12, 23, 40, 0.92);
      --border: rgba(148, 163, 184, 0.16);
      --text: #e7eefb;
      --text-dim: #93a7c4;
      --accent: #7dd3fc;
      --accent-2: #f59e0b;
      --add: #34d399;
      --remove: #fb7185;
      --context: #cbd5e1;
      --meta: #fbbf24;
      --shadow: 0 24px 60px rgba(0, 0, 0, 0.35);
    }
    * {
      box-sizing: border-box;
    }
    html, body {
      margin: 0;
      min-height: 100%%;
      background:
        radial-gradient(circle at top right, rgba(125, 211, 252, 0.18), transparent 24%%),
        radial-gradient(circle at left center, rgba(245, 158, 11, 0.12), transparent 18%%),
        linear-gradient(180deg, var(--bg), var(--bg-2));
      color: var(--text);
      font-family: "Space Grotesk", system-ui, sans-serif;
    }
    body {
      padding: 28px 18px 120px;
    }
    .shell {
      max-width: 1320px;
      margin: 0 auto;
      display: grid;
      gap: 18px;
    }
    .hero,
    .panel,
    .toolbar {
      border: 1px solid var(--border);
      background: var(--surface);
      backdrop-filter: blur(18px);
      box-shadow: var(--shadow);
    }
    .hero {
      border-radius: 28px;
      padding: 28px;
      display: grid;
      gap: 18px;
    }
    .eyebrow {
      font: 500 12px/1 "IBM Plex Mono", monospace;
      text-transform: uppercase;
      letter-spacing: 0.16em;
      color: var(--accent);
    }
    .hero-top {
      display: flex;
      justify-content: space-between;
      gap: 18px;
      align-items: start;
    }
    h1 {
      margin: 0;
      font-size: clamp(32px, 5vw, 56px);
      line-height: 0.96;
    }
    .subtitle {
      max-width: 780px;
      color: var(--text-dim);
      font-size: 15px;
      line-height: 1.7;
    }
    .pill-row {
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
    }
    .pill {
      min-width: 140px;
      padding: 14px 16px;
      border-radius: 18px;
      background: rgba(15, 23, 42, 0.72);
      border: 1px solid rgba(148, 163, 184, 0.14);
    }
    .pill-label {
      font: 500 11px/1 "IBM Plex Mono", monospace;
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: 0.12em;
    }
    .pill-value {
      margin-top: 10px;
      font-size: 28px;
      font-weight: 700;
    }
    .pill-value.add {
      color: var(--add);
    }
    .pill-value.remove {
      color: var(--remove);
    }
    .pill-value.context {
      color: var(--context);
    }
    .panel {
      border-radius: 24px;
      overflow: hidden;
    }
    .panel-header {
      display: flex;
      justify-content: space-between;
      gap: 18px;
      padding: 18px 22px;
      background: rgba(15, 23, 42, 0.74);
      border-bottom: 1px solid var(--border);
    }
    .panel-title {
      font-size: 20px;
      font-weight: 700;
    }
    .panel-meta {
      color: var(--text-dim);
      font: 400 12px/1.6 "IBM Plex Mono", monospace;
    }
    .diff-table {
      width: 100%%;
      overflow: auto;
      background:
        linear-gradient(180deg, rgba(7, 17, 29, 0.92), rgba(9, 20, 35, 0.96));
    }
    .diff-files {
      display: grid;
      gap: 18px;
      padding: 18px;
      background: linear-gradient(180deg, rgba(244, 247, 251, 0.98), rgba(237, 242, 248, 0.98));
    }
    .diff-layout {
      display: grid;
      grid-template-columns: 280px minmax(0, 1fr);
      gap: 18px;
      align-items: start;
    }
    .diff-sidebar {
      position: sticky;
      top: 18px;
      display: grid;
      gap: 14px;
      align-self: start;
    }
    .diff-mode-switch {
      display: inline-flex;
      gap: 8px;
      align-items: center;
      padding: 4px;
      border: 1px solid #d0d7de;
      border-radius: 10px;
      background: #ffffff;
      box-shadow: 0 10px 24px rgba(15, 23, 42, 0.06);
      justify-self: start;
    }
    .file-nav {
      display: grid;
      gap: 10px;
      padding: 14px;
      border: 1px solid #d0d7de;
      border-radius: 12px;
      background: #ffffff;
      box-shadow: 0 10px 24px rgba(15, 23, 42, 0.06);
      max-height: calc(100vh - 240px);
      overflow: auto;
    }
    .file-nav-item {
      display: grid;
      gap: 8px;
      padding: 10px 12px;
      border-radius: 10px;
      text-decoration: none;
      border: 1px solid transparent;
      background: #f6f8fa;
      transition: background 120ms ease, border-color 120ms ease, transform 120ms ease;
    }
    .file-nav-item:hover {
      background: #eef2f8;
      border-color: #d0d7de;
      transform: translateY(-1px);
    }
    .file-nav-item.is-active {
      background: #ddf4ff;
      border-color: #54aeff;
    }
    .file-nav-path {
      color: #24292f;
      font: 600 12px/1.5 "IBM Plex Mono", monospace;
      overflow-wrap: anywhere;
    }
    .file-nav-stats {
      display: inline-flex;
      gap: 10px;
      align-items: center;
      color: #57606a;
      font: 500 11px/1 "IBM Plex Mono", monospace;
    }
    .diff-main {
      min-width: 0;
      display: grid;
      gap: 18px;
    }
    .mode-button {
      border-radius: 8px;
      border: 1px solid transparent;
      padding: 8px 12px;
      background: transparent;
      color: #57606a;
      font: 600 12px/1 "IBM Plex Mono", monospace;
    }
    .mode-button.is-active {
      background: #0969da;
      color: #ffffff;
      box-shadow: none;
      transform: none;
    }
    .mode-panel.is-hidden {
      display: none;
    }
    .diff-row {
      display: grid;
      grid-template-columns: 56px 86px minmax(0, 1fr);
      align-items: start;
      border-bottom: 1px solid rgba(148, 163, 184, 0.08);
      font: 400 13px/1.65 "IBM Plex Mono", monospace;
    }
    .diff-row:last-child {
      border-bottom: none;
    }
    .diff-gutter,
    .diff-line,
    .diff-code {
      padding: 10px 14px;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .diff-gutter,
    .diff-line {
      color: var(--text-dim);
      user-select: none;
    }
    .diff-code {
      color: var(--text);
    }
    .diff-row.diff-add {
      background: linear-gradient(90deg, rgba(52, 211, 153, 0.12), rgba(52, 211, 153, 0.03));
    }
    .diff-row.diff-add .diff-gutter,
    .diff-row.diff-add .diff-code {
      color: #d3fff0;
    }
    .diff-row.diff-remove {
      background: linear-gradient(90deg, rgba(251, 113, 133, 0.12), rgba(251, 113, 133, 0.03));
    }
    .diff-row.diff-remove .diff-gutter,
    .diff-row.diff-remove .diff-code {
      color: #ffd7df;
    }
    .diff-row.diff-meta {
      background: linear-gradient(90deg, rgba(245, 158, 11, 0.12), rgba(245, 158, 11, 0.03));
    }
    .diff-row.diff-meta .diff-code {
      color: #fde7b0;
      font-weight: 500;
    }
    .diff-row.diff-title {
      background: rgba(125, 211, 252, 0.08);
    }
    .diff-row.diff-title .diff-code {
      color: #dff5ff;
      font-weight: 600;
    }
    .file-card {
      border: 1px solid #d0d7de;
      border-radius: 12px;
      overflow: hidden;
      background: #ffffff;
      box-shadow: 0 14px 30px rgba(15, 23, 42, 0.08);
    }
    .file-header {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      align-items: center;
      padding: 12px 16px;
      background: #f6f8fa;
      border-bottom: 1px solid #d8dee4;
    }
    .file-path {
      color: #24292f;
      font: 600 14px/1.4 "IBM Plex Mono", monospace;
      overflow-wrap: anywhere;
    }
    .file-stats {
      display: inline-flex;
      gap: 10px;
      align-items: center;
      color: #57606a;
      font: 500 12px/1 "IBM Plex Mono", monospace;
      white-space: nowrap;
    }
    .stat-add {
      color: #1a7f37;
    }
    .stat-remove {
      color: #cf222e;
    }
    .file-body {
      overflow: auto;
      background: #ffffff;
    }
    .gh-hunk {
      border-top: 1px solid #d8dee4;
    }
    .gh-hunk:first-child {
      border-top: none;
    }
    .gh-hunk-header {
      padding: 8px 16px;
      background: #ddf4ff;
      color: #0969da;
      border-bottom: 1px solid #b6e3ff;
      font: 500 12px/1.5 "IBM Plex Mono", monospace;
    }
    .gh-line {
      display: grid;
      grid-template-columns: 64px 64px 28px minmax(0, 1fr);
      border-bottom: 1px solid #d8dee4;
      font: 400 12px/1.55 "IBM Plex Mono", monospace;
      color: #24292f;
    }
    .gh-line:last-child {
      border-bottom: none;
    }
    .gh-no,
    .gh-prefix,
    .gh-code {
      padding: 2px 8px;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .gh-no,
    .gh-prefix {
      user-select: none;
    }
    .gh-no {
      text-align: right;
      color: #57606a;
      background: #f6f8fa;
      border-right: 1px solid #d8dee4;
    }
    .gh-prefix {
      text-align: center;
      color: #57606a;
      border-right: 1px solid #d8dee4;
      background: #f6f8fa;
    }
    .gh-code {
      color: #24292f;
      background: #ffffff;
    }
    .gh-add .gh-no,
    .gh-add .gh-prefix {
      background: #dafbe1;
    }
    .gh-add .gh-code {
      background: #eaffea;
    }
    .gh-remove .gh-no,
    .gh-remove .gh-prefix {
      background: #ffebe9;
    }
    .gh-remove .gh-code {
      background: #fff1ef;
    }
    .word-add {
      background: rgba(46, 160, 67, 0.28);
      border-radius: 3px;
      box-shadow: inset 0 0 0 1px rgba(26, 127, 55, 0.18);
    }
    .word-remove {
      background: rgba(207, 34, 46, 0.18);
      border-radius: 3px;
      box-shadow: inset 0 0 0 1px rgba(207, 34, 46, 0.16);
    }
    .tok-keyword {
      color: #8250df;
      font-weight: 600;
    }
    .tok-string {
      color: #0a7f32;
    }
    .tok-number {
      color: #0550ae;
    }
    .tok-comment {
      color: #6e7781;
      font-style: italic;
    }
    .gh-note .gh-code,
    .gh-meta .gh-code {
      color: #57606a;
      font-style: italic;
    }
    .empty-state {
      padding: 16px;
      color: #57606a;
      font: 500 13px/1.6 "IBM Plex Mono", monospace;
    }
    .split-hunk {
      background: #ffffff;
    }
    .split-row {
      display: grid;
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
      border-bottom: 1px solid #d8dee4;
    }
    .split-row:last-child {
      border-bottom: none;
    }
    .split-cell {
      display: grid;
      grid-template-columns: 64px 64px 28px minmax(0, 1fr);
      min-width: 0;
    }
    .split-left {
      border-right: 1px solid #d8dee4;
    }
    .split-empty {
      background: #f6f8fa;
      min-height: 24px;
    }
    .split-context .gh-no,
    .split-context .gh-prefix,
    .split-context .gh-code {
      background: #ffffff;
    }
    .split-note .gh-no,
    .split-note .gh-prefix,
    .split-note .gh-code,
    .split-meta .gh-no,
    .split-meta .gh-prefix,
    .split-meta .gh-code {
      background: #f6f8fa;
    }
    .toolbar {
      position: fixed;
      left: 18px;
      right: 18px;
      bottom: 18px;
      border-radius: 22px;
      padding: 14px 18px;
      display: flex;
      justify-content: space-between;
      gap: 16px;
      align-items: center;
      max-width: 1320px;
      margin: 0 auto;
    }
    .toolbar-wrap {
      position: sticky;
      bottom: 0;
    }
    .toolbar-copy {
      color: var(--text-dim);
      font-size: 14px;
      line-height: 1.6;
    }
    .toolbar-copy strong {
      color: var(--text);
    }
    .actions {
      display: flex;
      gap: 12px;
      flex-wrap: wrap;
    }
    button {
      appearance: none;
      border: none;
      border-radius: 14px;
      padding: 14px 18px;
      font: 600 14px/1 "Space Grotesk", system-ui, sans-serif;
      cursor: pointer;
      transition: transform 120ms ease, opacity 120ms ease, box-shadow 120ms ease;
    }
    button:hover {
      transform: translateY(-1px);
    }
    button:active {
      transform: translateY(0);
    }
    .approve {
      color: #04140f;
      background: linear-gradient(135deg, #34d399, #7dd3fc);
      box-shadow: 0 10px 24px rgba(52, 211, 153, 0.24);
    }
    .cancel {
      color: var(--text);
      background: rgba(15, 23, 42, 0.92);
      border: 1px solid rgba(148, 163, 184, 0.18);
    }
    .shortcut {
      color: var(--text-dim);
      font: 400 12px/1.6 "IBM Plex Mono", monospace;
    }
    @media (max-width: 900px) {
      body {
        padding: 18px 12px 144px;
      }
      .toolbar {
        left: 12px;
        right: 12px;
        bottom: 12px;
        flex-direction: column;
        align-items: stretch;
      }
      .hero-top,
      .panel-header {
        flex-direction: column;
      }
      .diff-row {
        grid-template-columns: 42px 68px minmax(0, 1fr);
      }
      .split-row {
        grid-template-columns: 1fr;
      }
      .diff-layout {
        grid-template-columns: 1fr;
      }
      .diff-sidebar {
        position: static;
      }
      .file-nav {
        max-height: none;
      }
      .split-left {
        border-right: none;
        border-bottom: 1px solid #d8dee4;
      }
      .split-cell {
        grid-template-columns: 48px 48px 24px minmax(0, 1fr);
      }
    }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <div class="eyebrow">Kernforge Review Surface</div>
      <div class="hero-top">
        <div>
          <h1>%s</h1>
          <div class="subtitle">%s</div>
        </div>
        <div class="shortcut">%s</div>
      </div>
      <div class="pill-row">
        <div class="pill"><div class="pill-label">Added</div><div class="pill-value add">%d</div></div>
        <div class="pill"><div class="pill-label">Removed</div><div class="pill-value remove">%d</div></div>
        <div class="pill"><div class="pill-label">Context</div><div class="pill-value context">%d</div></div>
        <div class="pill"><div class="pill-label">Visible lines</div><div class="pill-value">%d</div></div>
      </div>
    </section>

    <section class="panel">
      <div class="panel-header">
        <div class="panel-title">Diff Review</div>
        <div class="panel-meta">Local review window. No external upload or remote diff service is involved.</div>
      </div>
      <div class="%s">%s</div>
    </section>
  </div>

  <div class="toolbar-wrap">
    <div class="toolbar">
      <div class="toolbar-copy">%s</div>
      <div class="actions">%s</div>
    </div>
  </div>

  <script>
    document.querySelectorAll('.mode-button').forEach(function (button)
    {
      button.addEventListener('click', function ()
      {
        const mode = button.getAttribute('data-mode');
        document.querySelectorAll('.mode-button').forEach(function (item)
        {
          item.classList.toggle('is-active', item === button);
        });
        document.querySelectorAll('.mode-panel').forEach(function (panel)
        {
          panel.classList.toggle('is-hidden', panel.getAttribute('data-panel') !== mode);
        });
      });
    });
    const fileNavItems = Array.from(document.querySelectorAll('.file-nav-item'));
    fileNavItems.forEach(function (item)
    {
      item.addEventListener('click', function ()
      {
        fileNavItems.forEach(function (other)
        {
          other.classList.toggle('is-active', other === item);
        });
      });
    });
%s
  </script>
</body>
</html>`,
		htmlEscape(title),
		htmlEscape(title),
		htmlEscape(subtitle),
		shortcutLabel,
		metrics.Added,
		metrics.Removed,
		metrics.Context,
		len(lines),
		contentClass,
		contentHTML,
		toolbarCopy,
		actionHTML,
		opts.ActionScript,
	)
}
