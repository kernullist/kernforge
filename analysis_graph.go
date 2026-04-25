package main

import (
	"fmt"
	"regexp"
	"strings"
)

type analysisGraphEdgeView struct {
	Source     string
	Target     string
	Type       string
	Class      string
	Flow       string
	Confidence string
	Evidence   string
	Next       string
}

func analysisGraphProjectEdges(run ProjectAnalysisRun) []ProjectEdge {
	items := append([]ProjectEdge{}, run.Snapshot.ProjectEdges...)
	items = append(items, run.KnowledgePack.ProjectEdges...)
	return analysisUniqueProjectEdges(items)
}

func analysisGraphEdgeViews(run ProjectAnalysisRun) []analysisGraphEdgeView {
	out := []analysisGraphEdgeView{}
	for _, edge := range analysisGraphProjectEdges(run) {
		view := analysisGraphEdgeView{
			Source:     edge.Source,
			Target:     edge.Target,
			Type:       edge.Type,
			Class:      analysisGraphEdgeClass(edge),
			Flow:       analysisGraphEdgeFlow(edge),
			Confidence: edge.Confidence,
			Evidence:   strings.Join(limitStrings(edge.Evidence, 3), ", "),
			Next:       analysisGraphEdgeNextCommand(edge),
		}
		if strings.TrimSpace(view.Evidence) == "" {
			view.Evidence = firstNonBlankAnalysisString(edge.Attributes["source"], "inferred")
		}
		out = append(out, view)
	}
	return out
}

func analysisGraphTrustBoundaryViews(run ProjectAnalysisRun) []analysisGraphEdgeView {
	out := []analysisGraphEdgeView{}
	for _, view := range analysisGraphEdgeViews(run) {
		if view.Class == "trust_boundary" || containsAny(strings.ToLower(view.Flow+" "+view.Type), "ioctl", "rpc", "kernel", "user", "security", "integrity", "tamper") {
			out = append(out, view)
		}
	}
	return out
}

func analysisGraphDataFlowViews(run ProjectAnalysisRun) []analysisGraphEdgeView {
	out := []analysisGraphEdgeView{}
	for _, view := range analysisGraphEdgeViews(run) {
		if view.Class == "data_flow" || view.Class == "runtime" || view.Class == "asset" || view.Class == "config" {
			out = append(out, view)
		}
	}
	return out
}

func analysisGraphEdgeClass(edge ProjectEdge) string {
	text := strings.ToLower(strings.Join([]string{
		edge.Type,
		edge.Attributes["kind"],
		edge.Attributes["flow"],
		edge.Attributes["direction"],
		edge.Source,
		edge.Target,
	}, " "))
	switch {
	case containsAny(text, "trust", "security", "integrity", "tamper", "ioctl", "kernel", "user_to_kernel", "rpc"):
		return "trust_boundary"
	case strings.Contains(text, "runtime_edge") || containsAny(text, "dynamic_load", "process_spawn", "project_reference"):
		return "runtime"
	case strings.Contains(text, "config"):
		return "config"
	case strings.Contains(text, "asset"):
		return "asset"
	case strings.Contains(text, "dependency") || containsAny(text, "direction", "flow", "server_to", "client_to", "replicated", "input_action", "parser", "telemetry"):
		return "data_flow"
	case strings.Contains(text, "module"):
		return "build"
	case strings.Contains(text, "gameplay"):
		return "gameplay"
	default:
		return "relationship"
	}
}

func analysisGraphEdgeFlow(edge ProjectEdge) string {
	parts := []string{}
	for _, key := range []string{"flow", "direction", "kind", "system", "role", "type"} {
		value := strings.TrimSpace(edge.Attributes[key])
		if value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "type="+edge.Type)
	}
	return strings.Join(parts, " / ")
}

func analysisGraphEdgeNextCommand(edge ProjectEdge) string {
	text := strings.ToLower(strings.Join([]string{edge.Type, edge.Attributes["kind"], edge.Attributes["flow"], edge.Attributes["direction"], edge.Source, edge.Target}, " "))
	if containsAny(text, "fuzz", "parser", "ioctl", "rpc", "packet", "telemetry") {
		return "/fuzz-campaign run"
	}
	if containsAny(text, "security", "trust", "kernel", "tamper", "integrity") {
		return "/verify"
	}
	return "/analyze-dashboard"
}

func analysisGraphMarkdownTable(views []analysisGraphEdgeView, limit int) string {
	views = limitAnalysisGraphViews(views, limit)
	if len(views) == 0 {
		return "No graph edges inferred.\n"
	}
	var b strings.Builder
	b.WriteString("| Class | Source | Flow | Target | Confidence | Evidence | Next |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, view := range views {
		fmt.Fprintf(&b, "| %s | `%s` | %s | `%s` | %s | %s | `%s` |\n",
			markdownCell(view.Class),
			markdownCell(view.Source),
			markdownCell(view.Flow),
			markdownCell(view.Target),
			markdownCell(firstNonBlankAnalysisString(view.Confidence, "unknown")),
			markdownCell(firstNonBlankAnalysisString(view.Evidence, "inferred")),
			markdownCell(view.Next),
		)
	}
	return b.String()
}

func analysisGraphMermaid(views []analysisGraphEdgeView, limit int) string {
	views = limitAnalysisGraphViews(views, limit)
	if len(views) == 0 {
		return "No graph edges inferred.\n"
	}
	var b strings.Builder
	b.WriteString("```mermaid\ngraph LR\n")
	ids := map[string]string{}
	nodeID := func(label string) string {
		label = strings.TrimSpace(label)
		if id, ok := ids[label]; ok {
			return id
		}
		id := "n" + fmt.Sprintf("%02d", len(ids)+1)
		ids[label] = id
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", id, mermaidLabel(label))
		return id
	}
	for _, view := range views {
		sourceID := nodeID(view.Source)
		targetID := nodeID(view.Target)
		fmt.Fprintf(&b, "  %s -->|%s| %s\n", sourceID, mermaidLabel(firstNonBlankAnalysisString(view.Flow, view.Type)), targetID)
	}
	b.WriteString("```\n")
	return b.String()
}

func limitAnalysisGraphViews(items []analysisGraphEdgeView, limit int) []analysisGraphEdgeView {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return "unknown"
	}
	return value
}

var mermaidUnsafeLabel = regexp.MustCompile(`["<>]`)

func mermaidLabel(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = mermaidUnsafeLabel.ReplaceAllString(value, "'")
	if len(value) > 80 {
		value = value[:77] + "..."
	}
	return value
}
