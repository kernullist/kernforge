// KernForge VS Code extension entrypoint (Slice-1 scaffold).
//
// The extension is a read-only observer plus a thin UI:
//  - it connects to the KernForge daemon (auto-start optional),
//  - it watches the token-authed SSE stream of MCP RPC progress,
//  - it exposes "Analyze Selection" and "Review" commands that issue tools/call
//    requests through the daemon /rpc endpoint,
//  - it shows EditPreview-shaped responses in a PreviewWebView whose Apply action
//    routes back through the daemon, so every edit still passes the agent loop's
//    permission/review/edit gates.
//
// It never writes workspace files directly and never bypasses a gate.

import * as vscode from "vscode";
import { DaemonClient, DaemonState, StreamEvent } from "./daemonClient";
import { EditPreviewShape, looksLikeEditPreview, PreviewWebView } from "./previewWebView";

let output: vscode.OutputChannel;
let client: DaemonClient;
let preview: PreviewWebView;
let currentState: DaemonState | undefined;
let closeStream: (() => void) | undefined;

export function activate(context: vscode.ExtensionContext): void {
  output = vscode.window.createOutputChannel("KernForge");
  context.subscriptions.push(output);

  client = new DaemonClient({
    binaryPath: readConfigString("binaryPath"),
    autoStartDaemon: readConfigBool("autoStartDaemon", true),
    healthTimeoutMs: readConfigNumber("healthTimeoutMs", 2000),
    log: (message) => output.appendLine(message),
  });
  preview = new PreviewWebView(client, () => currentState, (message) => output.appendLine(message));

  context.subscriptions.push(
    vscode.commands.registerCommand("kernforge.connect", () => void connect()),
    vscode.commands.registerCommand("kernforge.disconnect", () => disconnect()),
    vscode.commands.registerCommand("kernforge.analyzeSelection", () => void analyzeSelection()),
    vscode.commands.registerCommand("kernforge.review", () => void review()),
  );
  context.subscriptions.push({ dispose: () => disconnect() });

  // Best-effort connect on activation; failures are logged, not fatal.
  void connect();
}

export function deactivate(): void {
  disconnect();
}

async function connect(): Promise<void> {
  try {
    const roots = workspaceRoots();
    currentState = await client.ensureRunning(roots);
    output.appendLine(`connected to daemon at ${currentState.addr}`);
    closeStream?.();
    closeStream = client.openStream(
      currentState,
      (event) => onStreamEvent(event),
      (err) => output.appendLine(`stream error: ${err.message}`),
    );
  } catch (err) {
    output.appendLine(`connect failed: ${describeError(err)}`);
    void vscode.window.showWarningMessage(`KernForge: could not connect to daemon: ${describeError(err)}`);
  }
}

function disconnect(): void {
  closeStream?.();
  closeStream = undefined;
  currentState = undefined;
}

function onStreamEvent(event: StreamEvent): void {
  // Observe-only: render progress to the output channel. A richer status view is
  // deferred to a later slice.
  const tool = typeof event.data.tool === "string" ? ` ${event.data.tool}` : "";
  const method = typeof event.data.method === "string" ? event.data.method : event.event;
  output.appendLine(`[stream] ${event.event} ${method}${tool}`);
}

async function analyzeSelection(): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    void vscode.window.showInformationMessage("KernForge: open a file and select code to analyze.");
    return;
  }
  const selection = editor.selection;
  const message = {
    jsonrpc: "2.0",
    id: Date.now(),
    method: "tools/call",
    params: {
      name: "kernforge_analyze_project",
      // Pass IDE context the daemon already understands. activeSelection carries
      // the selected range; workspaceRoot resolves the daemon runtime. A bare
      // file uri in the selection is ignored by workspace resolution by design.
      clientContext: {
        workspaceRoot: workspaceUriForEditor(editor),
        activeSelection: {
          uri: editor.document.uri.toString(),
          range: {
            start: { line: selection.start.line, character: selection.start.character },
            end: { line: selection.end.line, character: selection.end.character },
          },
        },
      },
      arguments: {},
    },
  };
  await runToolCall("kernforge_analyze_project", message, workspaceRootForEditor(editor));
}

async function review(): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  const message = {
    jsonrpc: "2.0",
    id: Date.now(),
    method: "tools/call",
    params: {
      name: "kernforge_verify",
      clientContext: editor
        ? { workspaceRoot: workspaceUriForEditor(editor) }
        : {},
      arguments: {},
    },
  };
  await runToolCall("kernforge_verify", message, editor ? workspaceRootForEditor(editor) : undefined);
}

async function runToolCall(
  toolName: string,
  message: Record<string, unknown>,
  workspace: string | undefined,
): Promise<void> {
  if (!currentState) {
    await connect();
  }
  if (!currentState) {
    void vscode.window.showErrorMessage("KernForge: daemon not connected.");
    return;
  }
  try {
    const result = await client.callRpc(currentState, message, workspace);
    if (result.error) {
      void vscode.window.showErrorMessage(`KernForge ${toolName} error: ${result.error}`);
      return;
    }
    const text = extractToolText(result.response);
    if (text.length === 0) {
      void vscode.window.showInformationMessage(`KernForge ${toolName}: no output.`);
      return;
    }
    if (looksLikeEditPreview(text)) {
      const shape: EditPreviewShape = { text, toolName, workspace };
      preview.show(shape);
      return;
    }
    output.appendLine(`[${toolName}] ${text}`);
    output.show(true);
  } catch (err) {
    void vscode.window.showErrorMessage(`KernForge ${toolName} failed: ${describeError(err)}`);
  }
}

// extractToolText pulls the first text content block out of an MCP tools/call
// result envelope. It is defensive: any shape it does not recognize yields "".
function extractToolText(response: Record<string, unknown> | undefined): string {
  if (!response) {
    return "";
  }
  const result = response.result;
  if (!isRecord(result)) {
    return "";
  }
  const content = result.content;
  if (!Array.isArray(content)) {
    return "";
  }
  for (const block of content) {
    if (isRecord(block) && block.type === "text" && typeof block.text === "string") {
      return block.text;
    }
  }
  return "";
}

function workspaceRoots(): string[] {
  const folders = vscode.workspace.workspaceFolders ?? [];
  return folders.map((folder) => folder.uri.fsPath);
}

function workspaceUriForEditor(editor: vscode.TextEditor): string | undefined {
  const folder = vscode.workspace.getWorkspaceFolder(editor.document.uri);
  return folder ? folder.uri.toString() : undefined;
}

function workspaceRootForEditor(editor: vscode.TextEditor): string | undefined {
  const folder = vscode.workspace.getWorkspaceFolder(editor.document.uri);
  return folder ? folder.uri.fsPath : undefined;
}

function readConfigString(key: string): string {
  return vscode.workspace.getConfiguration("kernforge").get<string>(key, "");
}

function readConfigBool(key: string, fallback: boolean): boolean {
  return vscode.workspace.getConfiguration("kernforge").get<boolean>(key, fallback);
}

function readConfigNumber(key: string, fallback: number): number {
  return vscode.workspace.getConfiguration("kernforge").get<number>(key, fallback);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function describeError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
