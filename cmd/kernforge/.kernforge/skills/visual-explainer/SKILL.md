---
name: visual-explainer
description: Generate beautiful, self-contained HTML pages that visually explain systems, code changes, plans, and data — a KernForge built-in skill. Use when the user asks for a diagram, architecture overview, diff review, plan review, project recap, comparison table, or any visual explanation of technical concepts. Also use proactively when you are about to render a complex ASCII table (4+ rows or 3+ columns) — write a styled HTML page instead. Triggers on "diagram", "시각화", "다이어그램", "아키텍처 그림", "비교표", "visual explanation".
---

# Visual Explainer (KernForge built-in)

Generate self-contained HTML files for technical diagrams, visualizations, and data tables. Write the file with `write_file`, tell the user the path, and open it in the browser when possible. Never fall back to ASCII art when this skill is active.

## KernForge tool mapping — read first

- **Read the reference templates** with `read_file` before generating (paths below are relative to this skill's directory).
- **Write the HTML** with `write_file` to `.kernforge/diagrams/<descriptive-name>.html` in the workspace. There is no separate Artifact tool — the deliverable is a single self-contained `.html` file on disk.
- **Open in the browser** with `run_shell`, choosing the platform opener:
  - Windows: `cmd /c start "" ".kernforge/diagrams/<name>.html"`
  - macOS: `open .kernforge/diagrams/<name>.html`
  - Linux: `xdg-open .kernforge/diagrams/<name>.html`
  If opening is not permitted or fails, just report the absolute file path so the user can open it.
- KernForge also has an internal viewer: the user can run `/open <path>` to view the file. Mention this as a fallback.

**Proactive table rendering.** When you are about to present tabular data as an ASCII box-drawing table in the terminal (comparisons, audits, feature matrices, status reports, any structured rows/columns), write an HTML page instead. The threshold: if the table has 4+ rows or 3+ columns, it belongs in the browser. Do not wait for the user to ask — render it as HTML automatically and tell them the file path. A brief text summary in the chat is fine, but the table itself should be the HTML page.

## Workflow

### 1. Think (5 seconds, not 5 minutes)

Before writing HTML, commit to a direction. Do not default to "dark theme with blue accents" every time.

**Who is looking?** A developer understanding a system? A PM seeing the big picture? A team reviewing a proposal? This shapes information density and visual complexity.

**What type of diagram?** Architecture, flowchart, sequence, data flow, schema/ER, state machine, mind map, data table, timeline, or dashboard. Each has distinct layout needs (see Diagram Types below).

**What aesthetic?** Pick one and commit:
- Monochrome terminal (green/amber on black, monospace everything)
- Editorial (serif headlines, generous whitespace, muted palette)
- Blueprint (technical drawing feel, grid lines, precise)
- Neon dashboard (saturated accents on deep dark, glowing edges)
- Paper/ink (warm cream background, hand-drawn feel, sketchy borders)
- Hand-drawn / sketch (Mermaid `handDrawn` mode, wiggly lines, informal whiteboard feel)
- IDE-inspired (borrow a real color scheme: Dracula, Nord, Catppuccin, Solarized, Gruvbox, One Dark)
- Data-dense (small type, tight spacing, maximum information)
- Gradient mesh (bold gradients, glassmorphism, modern SaaS feel)

Vary the choice each time. If the last diagram was dark and technical, make the next one light and editorial. The swap test: if you replaced your styling with a generic dark theme and nobody would notice the difference, you have not designed anything.

### 2. Structure

**Read the reference template** before generating. Do not memorize it — read it each time with `read_file` to absorb the patterns.
- For text-heavy architecture overviews (card content matters more than topology): read `templates/architecture.html`
- For flowcharts, sequence diagrams, ER, state machines, mind maps: read `templates/mermaid-flowchart.html`
- For data tables, comparisons, audits, feature matrices: read `templates/data-table.html`

**For CSS/layout patterns and SVG connectors**, read `references/css-patterns.md`.

**For pages with 4+ sections** (reviews, recaps, dashboards), also read `references/responsive-nav.md` for section navigation with a sticky sidebar TOC on desktop and a horizontal scrollable bar on mobile.

**Choosing a rendering approach:**

| Diagram type | Approach | Why |
|---|---|---|
| Architecture (text-heavy) | CSS Grid cards + flow arrows | Rich card content (descriptions, code, tool lists) needs CSS control |
| Architecture (topology-focused) | **Mermaid** | Visible connections between components need automatic edge routing |
| Flowchart / pipeline | **Mermaid** | Automatic node positioning and edge routing; hand-drawn mode available |
| Sequence diagram | **Mermaid** | Lifelines, messages, and activation boxes need automatic layout |
| Data flow | **Mermaid** with edge labels | Connections and data descriptions need automatic edge routing |
| ER / schema diagram | **Mermaid** | Relationship lines between many entities need auto-routing |
| State machine | **Mermaid** | State transitions with labeled edges need automatic layout |
| Mind map | **Mermaid** | Hierarchical branching needs automatic positioning |
| Data table | HTML `<table>` | Semantic markup, accessibility, copy-paste behavior |
| Timeline | CSS (central line + cards) | Simple linear layout does not need a layout engine |
| Dashboard | CSS Grid + Chart.js | Card grid with embedded charts |

**Mermaid theming:** Always use `theme: 'base'` with custom `themeVariables` so colors match your page palette. Use `look: 'handDrawn'` for a sketch aesthetic or `look: 'classic'` for clean lines. Use `layout: 'elk'` for complex graphs. Override Mermaid's SVG classes with CSS for pixel-perfect control. See `references/libraries.md` for the full theming guide and CDN imports.

**Mermaid zoom controls:** Always add zoom controls (+/-/reset buttons) to every `.mermaid-wrap` container, plus Ctrl/Cmd+scroll zoom and click-and-drag panning. Complex diagrams render small and need zoom to be readable. See the zoom-controls pattern in `references/css-patterns.md` and `templates/mermaid-flowchart.html`.

**Self-contained requirement.** The `.html` file must stand on its own: inline all CSS and JS. CDN links for fonts and libraries (Mermaid, Chart.js, anime.js) are the only allowed external references. Embed any raster images as base64 `data:` URIs so the file works when moved or shared.

**AI-generated illustrations (optional).** These are optional and must degrade gracefully. If an image-generation CLI is available on the machine, you may generate images and embed them as base64 `data:` URIs. Check availability first (for example `run_shell` with `where surf` on Windows or `which surf` on Unix); if it is not available, skip images without erroring. The page must look intentional with CSS and typography alone. Use images only for hero banners, conceptual illustrations Mermaid cannot express, or educational/decorative accents — never for data-heavy pages.

### 3. Style

Apply these to every diagram:

**Typography is the diagram.** Pick a distinctive font pairing from Google Fonts — a display/heading font with character plus a mono font for technical labels. Never use Inter, Roboto, Arial, or system-ui as the primary font. Load via `<link>` in `<head>` with a system fallback in the `font-family` stack.

**Color tells a story.** Use CSS custom properties for the full palette: at minimum `--bg`, `--surface`, `--border`, `--text`, `--text-dim`, and 3-5 accent colors, each with a full and a dim variant. Name variables semantically (`--pipeline-step`, not `--blue-3`). Support both themes — primary aesthetic in `:root`, alternate in the media query:

```css
/* Light-first (editorial, paper/ink, blueprint): */
:root { /* light values */ }
@media (prefers-color-scheme: dark) { :root { /* dark values */ } }

/* Dark-first (neon, IDE-inspired, terminal): */
:root { /* dark values */ }
@media (prefers-color-scheme: light) { :root { /* light values */ } }
```

**Surfaces whisper.** Build depth through subtle lightness shifts (2-4% between levels), not dramatic color changes. Borders should be low-opacity rgba (`rgba(255,255,255,0.08)` in dark, `rgba(0,0,0,0.08)` in light).

**Backgrounds create atmosphere.** No flat solid page background. Use subtle gradients, faint CSS grid patterns, or gentle radial glows behind focal areas.

**Visual weight signals importance.** Not every section deserves equal treatment. Executive summaries and key metrics dominate on load (larger type, more padding, accent-tinted zone). Reference sections (file maps, decision logs) stay compact. Use `<details>/<summary>` for useful-but-secondary sections (pattern in `references/css-patterns.md`).

**Animation earns its place.** Staggered fade-ins on load guide the eye. Mix types by role: `fadeUp` for cards, `fadeScale` for KPIs/badges, `drawIn` for SVG connectors, `countUp` for hero numbers. Always respect `prefers-reduced-motion`. CSS handles most cases; anime.js via CDN is available for orchestrated sequences (see `references/libraries.md`).

### 4. Deliver

1. `write_file` the HTML to `.kernforge/diagrams/<descriptive-name>.html` (e.g. `driver-architecture.html`, `pipeline-flow.html`).
2. Open it with the platform opener via `run_shell` (see tool mapping above), or report the absolute path if opening is not allowed.
3. Tell the user the file path so they can re-open or share it, and mention `/open <path>` as an in-app viewer fallback.

## Diagram Types

### Architecture / System Diagrams
**Text-heavy overviews** (card content matters more than connections): CSS Grid with explicit row/column placement, rounded cards with colored borders and monospace labels, vertical flow arrows, nested grids for subsystems. See `templates/architecture.html`. Use when cards need descriptions, code references, or tool lists.

**Topology-focused diagrams** (connections matter more than card content): **use Mermaid.** A `graph TD` or `graph LR` with custom `themeVariables` produces proper diagrams with automatic edge routing.

### Flowcharts / Pipelines
**Use Mermaid.** Automatic node positioning and edge routing beat CSS flexbox with arrow characters. `graph TD` for top-down, `graph LR` for left-right. `look: 'handDrawn'` for sketch feel. Color-code node types with `classDef` or `themeVariables`.

### Sequence Diagrams
**Use Mermaid** `sequenceDiagram`. Lifelines, messages, activation boxes, notes, and loops need automatic layout. Style actors and messages via CSS overrides on `.actor`, `.messageText`, `.activation`.

### Data Flow Diagrams
**Use Mermaid** `graph LR`/`graph TD` with edge labels for data descriptions. Thicker, colored edges for primary flows. Source/sink nodes styled differently from transform nodes via `classDef`.

### Schema / ER Diagrams
**Use Mermaid** `erDiagram` with entity attributes. Style via `themeVariables` and CSS overrides on `.er.entityBox` and `.er.relationshipLine`.

### State Machines / Decision Trees
**Use Mermaid** `stateDiagram-v2` for states with labeled transitions (supports nested states, forks, joins, notes). Decision trees can use `graph TD` with diamond nodes.

**`stateDiagram-v2` label caveat:** transition labels have a strict parser — colons, parentheses, `<br/>`, HTML entities, and most special characters cause silent parse failures. If labels need any of these (e.g. `cancel()`, `curate: true`, multi-line), use `flowchart LR` instead with rounded nodes and quoted edge labels (`|"label text"|`), which handles all special characters and `<br/>`. Reserve `stateDiagram-v2` for simple single-word or plain-text labels.

### Mind Maps / Hierarchical Breakdowns
**Use Mermaid** `mindmap` for hierarchical branching from a root node. Style with `themeVariables` per depth level.

### Data Tables / Comparisons / Audits
Use a real `<table>` element — not CSS Grid pretending to be a table. Tables get accessibility, copy-paste, and column alignment for free. See `templates/data-table.html`.

**Use proactively.** Any time you would render an ASCII box-drawing table in the terminal, write an HTML table instead: requirement audits, feature comparisons, status reports, configuration matrices, test result summaries, dependency lists, permission tables, API inventories.

Layout: sticky `<thead>`; alternating row backgrounds via `tr:nth-child(even)` (subtle 2-3% shift); first column optionally sticky for wide tables; responsive wrapper with `overflow-x: auto`; column width hints via `<colgroup>`; row hover highlight.

Status indicators (styled `<span>`, never emoji): pass/yes = green dot/checkmark; fail/no = red dot/cross; partial/warning = amber; neutral/info = dim muted badge.

Cell content: wrap long text naturally (do not truncate); `<code>` for technical references; secondary detail in `<small>` dimmed; numeric columns right-aligned with `tabular-nums`.

### Timeline / Roadmap Views
Vertical or horizontal timeline with a central line (CSS pseudo-element), phase markers as circles, content cards branching left/right (alternating) or to one side, date labels on the line, color progression from muted (past) to vivid (future).

### Dashboard / Metrics Overview
Card grid. Hero numbers large. Sparklines via inline SVG `<polyline>`. Progress bars via CSS `linear-gradient` on a div. For real charts (bar, line, pie), use **Chart.js via CDN** (see `references/libraries.md`). KPI cards with trend indicators.

## File Structure

Every diagram is a single self-contained `.html` file. No external assets except CDN links (fonts, optional libraries). Skeleton:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Descriptive Title</title>
  <link href="https://fonts.googleapis.com/css2?family=...&display=swap" rel="stylesheet">
  <style>
    /* CSS custom properties, theme, layout, components — all inline */
  </style>
</head>
<body>
  <!-- Semantic HTML: sections, headings, lists, tables, inline SVG -->
  <!-- Optional: <script> for Mermaid, Chart.js, or anime.js when used -->
</body>
</html>
```

## Quality Checks

Before delivering, verify:
- **The squint test**: blur your eyes. Can you still perceive hierarchy? Are sections visually distinct?
- **The swap test**: would replacing your fonts and colors with a generic dark theme make this indistinguishable from a template? If yes, push the aesthetic further.
- **Both themes**: both light and dark should look intentional, not broken.
- **Information completeness**: does the diagram actually convey what the user asked for? Pretty but incomplete is a failure.
- **No overflow**: at different widths, no content clips or escapes its container. Every grid/flex child needs `min-width: 0`; side-by-side panels need `overflow-wrap: break-word`. Never use `display: flex` on `<li>` for marker characters — use absolute positioning for markers instead. See the Overflow Protection section in `references/css-patterns.md`.
- **Mermaid zoom controls**: every `.mermaid-wrap` has +/-/reset buttons, Ctrl/Cmd+scroll zoom, and click-and-drag panning; cursor changes to `grab`/`grabbing`.
- **Self-contained**: opens cleanly with no external assets beyond CDN links; no console errors, no broken font loads, no layout shifts.
