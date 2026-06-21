// PreviewWebView renders an EditPreview-shaped daemon response and offers
// Apply / Cancel. It is strictly a UI: Apply does NOT write any file. It posts a
// follow-up tools/call back through the daemon /rpc endpoint (carrying the user's
// approval choice), and the daemon runs that call through the agent loop's
// permission/review/edit gates. Cancel simply disposes the panel. The webview
// therefore never holds an edit authority and cannot bypass a gate.

import * as vscode from "vscode";
import { DaemonClient, DaemonState } from "./daemonClient";

// EditPreviewShape is the minimal interpretation of an EditPreview-bearing tool
// response. The daemon returns tool output as text content; when that text looks
// like a unified-diff edit preview we surface it here. A real richer schema can
// replace this heuristic later without changing the Apply routing contract.
export interface EditPreviewShape {
  // text is the raw preview body (typically a unified diff plus a summary).
  text: string;
  // toolName is the tool that produced the preview, used to build the Apply
  // follow-up call.
  toolName: string;
  // workspace is the workspace root the preview belongs to, forwarded so the
  // Apply call resolves the same daemon runtime.
  workspace?: string;
}

// looksLikeEditPreview is a conservative heuristic: a tool text result that
// contains unified-diff hunk markers is treated as an edit preview. Anything
// else is shown as plain output, never as an actionable preview.
export function looksLikeEditPreview(text: string): boolean {
  if (text.length === 0) {
    return false;
  }
  return /^@@ .* @@/m.test(text) || /^(---|\+\+\+) /m.test(text);
}

export class PreviewWebView {
  private panel: vscode.WebviewPanel | undefined;

  constructor(
    private readonly client: DaemonClient,
    private readonly stateProvider: () => DaemonState | undefined,
    private readonly log: (message: string) => void,
  ) {}

  // show renders a preview and wires the Apply/Cancel message handler.
  show(preview: EditPreviewShape): void {
    if (!this.panel) {
      this.panel = vscode.window.createWebviewPanel(
        "kernforgePreview",
        "KernForge Edit Preview",
        vscode.ViewColumn.Beside,
        { enableScripts: true, retainContextWhenHidden: true },
      );
      this.panel.onDidDispose(() => {
        this.panel = undefined;
      });
      this.panel.webview.onDidReceiveMessage((message: { command?: string }) => {
        void this.onMessage(message, preview);
      });
    }
    this.panel.webview.html = renderPreviewHtml(preview.text);
    this.panel.reveal(vscode.ViewColumn.Beside);
  }

  private async onMessage(message: { command?: string }, preview: EditPreviewShape): Promise<void> {
    if (message.command === "cancel") {
      this.panel?.dispose();
      return;
    }
    if (message.command !== "apply") {
      return;
    }
    const state = this.stateProvider();
    if (!state) {
      void vscode.window.showErrorMessage("KernForge daemon is not connected.");
      return;
    }
    // Apply is expressed as a normal tools/call with execute approval. The daemon
    // still enforces every gate; this is not a privileged write.
    const applyCall = {
      jsonrpc: "2.0",
      id: Date.now(),
      method: "tools/call",
      params: {
        name: preview.toolName,
        arguments: {
          execution_mode: "execute",
          approve_recovered_build: false,
        },
      },
    };
    try {
      const result = await this.client.callRpc(state, applyCall, preview.workspace);
      if (result.error) {
        void vscode.window.showErrorMessage(`KernForge apply rejected: ${result.error}`);
        return;
      }
      this.log(`apply routed back through daemon for tool ${preview.toolName}`);
      void vscode.window.showInformationMessage("KernForge: apply request sent through the review gate.");
      this.panel?.dispose();
    } catch (err) {
      void vscode.window.showErrorMessage(`KernForge apply failed: ${describeError(err)}`);
    }
  }
}

function renderPreviewHtml(previewText: string): string {
  const escaped = escapeHtml(previewText);
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<style>
  body { font-family: var(--vscode-editor-font-family, monospace); padding: 8px; }
  pre { white-space: pre-wrap; word-break: break-word; overflow-x: auto; }
  .actions { position: sticky; top: 0; padding: 8px 0; background: var(--vscode-editor-background); }
  button { margin-right: 8px; padding: 4px 12px; }
  .note { color: var(--vscode-descriptionForeground); font-size: 0.9em; }
</style>
</head>
<body>
  <div class="actions">
    <button id="apply">Apply</button>
    <button id="cancel">Cancel</button>
    <div class="note">Apply routes back through the KernForge review and permission gate. The extension never writes files directly.</div>
  </div>
  <pre>${escaped}</pre>
  <script>
    const vscode = acquireVsCodeApi();
    document.getElementById("apply").addEventListener("click", () => vscode.postMessage({ command: "apply" }));
    document.getElementById("cancel").addEventListener("click", () => vscode.postMessage({ command: "cancel" }));
  </script>
</body>
</html>`;
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function describeError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
