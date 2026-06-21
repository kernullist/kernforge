// DaemonClient is the extension's read-only bridge to the KernForge daemon.
//
// Responsibilities (Slice-1):
//  - locate the kernforge binary (config override, PATH, common build outputs),
//  - read the daemon state file the Go daemon writes (addr + token),
//  - probe GET /health and optionally auto-start the daemon,
//  - open the token-authed GET /stream Server-Sent Events channel and surface
//    observe-only RPC progress events,
//  - POST /rpc (tools/call) on behalf of a UI action.
//
// The client is a UI/observer. It never holds an edit authority: an Apply action
// is expressed as a normal tools/call routed through /rpc, which the daemon still
// runs through the agent loop's permission/review/edit gates. The stream is
// observe-only and cannot mutate the workspace.

import * as http from "http";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as cp from "child_process";

// DaemonState mirrors the JSON the Go daemon persists at
// <home>/.kernforge/daemon/daemon.json. Only the fields the extension needs are
// declared; unknown fields are ignored.
export interface DaemonState {
  addr: string;
  token: string;
  pid?: number;
  log_path?: string;
}

// StreamEvent is one decoded Server-Sent Event from GET /stream. The Go side
// encodes the SSE id, event-type, and a single JSON data line.
export interface StreamEvent {
  id: string;
  event: string;
  data: Record<string, unknown>;
}

// RpcResponse is the envelope the daemon's /rpc endpoint returns.
export interface RpcResponse {
  respond: boolean;
  response?: Record<string, unknown>;
  error?: string;
}

export type StreamEventHandler = (event: StreamEvent) => void;
export type StreamErrorHandler = (error: Error) => void;

export interface DaemonClientOptions {
  // binaryPath, when set, is used verbatim instead of searching for the binary.
  binaryPath?: string;
  // autoStartDaemon controls whether ensureRunning may spawn "kernforge daemon
  // start" when the daemon is not reachable.
  autoStartDaemon: boolean;
  // healthTimeoutMs bounds the /health probe.
  healthTimeoutMs: number;
  // log receives human-readable diagnostics (wired to an OutputChannel).
  log: (message: string) => void;
}

export class DaemonClient {
  private readonly options: DaemonClientOptions;
  private streamRequest: http.ClientRequest | undefined;
  private closed = false;

  constructor(options: DaemonClientOptions) {
    this.options = options;
  }

  // statePath returns the daemon state file path the Go daemon writes.
  static statePath(): string {
    return path.join(os.homedir(), ".kernforge", "daemon", "daemon.json");
  }

  // readState reads and parses the daemon state file. It returns undefined when
  // the file is missing or malformed (daemon not started yet).
  static readState(): DaemonState | undefined {
    const file = DaemonClient.statePath();
    let raw: string;
    try {
      raw = fs.readFileSync(file, "utf8");
    } catch {
      return undefined;
    }
    try {
      const parsed = JSON.parse(raw) as Partial<DaemonState>;
      if (typeof parsed.addr === "string" && parsed.addr.length > 0 &&
        typeof parsed.token === "string" && parsed.token.length > 0) {
        return { addr: parsed.addr, token: parsed.token, pid: parsed.pid, log_path: parsed.log_path };
      }
    } catch {
      return undefined;
    }
    return undefined;
  }

  // locateBinary resolves the kernforge binary path. Order: explicit config,
  // then PATH (via the platform executable name), then common build outputs
  // relative to a workspace folder. It returns undefined when nothing is found.
  locateBinary(workspaceRoots: string[]): string | undefined {
    const configured = (this.options.binaryPath ?? "").trim();
    if (configured.length > 0 && fs.existsSync(configured)) {
      return configured;
    }
    const exe = process.platform === "win32" ? "kernforge.exe" : "kernforge";
    const pathDirs = (process.env.PATH ?? "").split(path.delimiter);
    for (const dir of pathDirs) {
      if (dir.trim().length === 0) {
        continue;
      }
      const candidate = path.join(dir, exe);
      if (fs.existsSync(candidate)) {
        return candidate;
      }
    }
    for (const root of workspaceRoots) {
      const candidates = [
        path.join(root, exe),
        path.join(root, "bin", exe),
        path.join(root, "cmd", "kernforge", exe),
      ];
      for (const candidate of candidates) {
        if (fs.existsSync(candidate)) {
          return candidate;
        }
      }
    }
    return undefined;
  }

  // health probes GET /health and resolves with the parsed body, or rejects on a
  // non-200 / transport error / timeout.
  async health(state: DaemonState): Promise<Record<string, unknown>> {
    return new Promise<Record<string, unknown>>((resolve, reject) => {
      const req = http.get(
        { host: hostOf(state.addr), port: portOf(state.addr), path: "/health", timeout: this.options.healthTimeoutMs },
        (res) => {
          collectBody(res, (status, body) => {
            if (status !== 200) {
              reject(new Error(`daemon /health returned ${status}`));
              return;
            }
            try {
              resolve(JSON.parse(body) as Record<string, unknown>);
            } catch (err) {
              reject(asError(err));
            }
          });
        },
      );
      req.on("timeout", () => req.destroy(new Error("daemon /health timed out")));
      req.on("error", (err) => reject(err));
    });
  }

  // ensureRunning returns a reachable DaemonState. It reads the state file and
  // probes health; when unreachable and autoStartDaemon is enabled it spawns
  // "kernforge daemon start" and waits (bounded) for readiness.
  async ensureRunning(workspaceRoots: string[]): Promise<DaemonState> {
    const existing = DaemonClient.readState();
    if (existing) {
      try {
        await this.health(existing);
        return existing;
      } catch {
        this.options.log("daemon state exists but is not reachable; will consider auto-start");
      }
    }
    if (!this.options.autoStartDaemon) {
      throw new Error("KernForge daemon is not running and auto-start is disabled");
    }
    const binary = this.locateBinary(workspaceRoots);
    if (!binary) {
      throw new Error("could not locate the kernforge binary; set kernforge.binaryPath");
    }
    this.options.log(`starting daemon via ${binary} daemon start`);
    await this.spawnDaemonStart(binary, workspaceRoots[0]);
    return this.waitForReady(5000);
  }

  private spawnDaemonStart(binary: string, cwd: string | undefined): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      const child = cp.spawn(binary, ["daemon", "start"], {
        cwd: cwd,
        stdio: "ignore",
        detached: false,
      });
      child.on("error", (err) => reject(err));
      child.on("exit", () => resolve());
    });
  }

  private async waitForReady(timeoutMs: number): Promise<DaemonState> {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const state = DaemonClient.readState();
      if (state) {
        try {
          await this.health(state);
          return state;
        } catch {
          // keep polling
        }
      }
      await delay(150);
    }
    throw new Error("daemon did not become ready in time");
  }

  // openStream connects to GET /stream and dispatches decoded events. It returns
  // a disposer that aborts the connection. The token is passed as a query
  // parameter because the SSE client cannot rely on custom headers everywhere;
  // the daemon also accepts an Authorization: Bearer header, used here as well.
  openStream(state: DaemonState, onEvent: StreamEventHandler, onError: StreamErrorHandler): () => void {
    this.closed = false;
    const req = http.get(
      {
        host: hostOf(state.addr),
        port: portOf(state.addr),
        path: `/stream?token=${encodeURIComponent(state.token)}`,
        headers: {
          Accept: "text/event-stream",
          Authorization: `Bearer ${state.token}`,
        },
      },
      (res) => {
        if (res.statusCode !== 200) {
          onError(new Error(`daemon /stream returned ${res.statusCode ?? 0}`));
          res.resume();
          return;
        }
        res.setEncoding("utf8");
        let buffer = "";
        res.on("data", (chunk: string) => {
          buffer += chunk;
          let sep = buffer.indexOf("\n\n");
          while (sep >= 0) {
            const block = buffer.slice(0, sep);
            buffer = buffer.slice(sep + 2);
            const event = parseSSEBlock(block);
            if (event) {
              onEvent(event);
            }
            sep = buffer.indexOf("\n\n");
          }
        });
        res.on("end", () => {
          if (!this.closed) {
            onError(new Error("daemon stream ended"));
          }
        });
      },
    );
    req.on("error", (err) => {
      if (!this.closed) {
        onError(err);
      }
    });
    this.streamRequest = req;
    return () => this.closeStream();
  }

  closeStream(): void {
    this.closed = true;
    if (this.streamRequest) {
      this.streamRequest.destroy();
      this.streamRequest = undefined;
    }
  }

  // callRpc posts a JSON-RPC message to /rpc with the daemon token. This is how
  // a UI action (Analyze, Review, Apply) reaches a tool. The daemon runs the
  // tool through the agent loop's permission/review/edit gates; the extension
  // never bypasses them.
  async callRpc(
    state: DaemonState,
    message: Record<string, unknown>,
    workspace?: string,
  ): Promise<RpcResponse> {
    const payload = JSON.stringify({
      token: state.token,
      workspace: workspace ?? "",
      workspace_source: workspace ? "ide" : "",
      message,
    });
    return new Promise<RpcResponse>((resolve, reject) => {
      const req = http.request(
        {
          host: hostOf(state.addr),
          port: portOf(state.addr),
          path: "/rpc",
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "Content-Length": Buffer.byteLength(payload),
          },
        },
        (res) => {
          collectBody(res, (status, body) => {
            if (status !== 200) {
              reject(new Error(`daemon /rpc returned ${status}: ${body.trim()}`));
              return;
            }
            try {
              resolve(JSON.parse(body) as RpcResponse);
            } catch (err) {
              reject(asError(err));
            }
          });
        },
      );
      req.on("error", (err) => reject(err));
      req.write(payload);
      req.end();
    });
  }
}

// parseSSEBlock decodes a single SSE event block (lines until a blank line). It
// skips comment lines (": ...") and concatenates multiple data: lines per spec.
function parseSSEBlock(block: string): StreamEvent | undefined {
  let id = "";
  let event = "message";
  const dataLines: string[] = [];
  for (const rawLine of block.split("\n")) {
    const line = rawLine.replace(/\r$/, "");
    if (line.length === 0 || line.startsWith(":")) {
      continue;
    }
    if (line.startsWith("id:")) {
      id = line.slice(3).trimStart();
    } else if (line.startsWith("event:")) {
      event = line.slice(6).trimStart();
    } else if (line.startsWith("data:")) {
      dataLines.push(line.slice(5).trimStart());
    }
  }
  if (dataLines.length === 0 && event === "message") {
    return undefined;
  }
  let data: Record<string, unknown> = {};
  if (dataLines.length > 0) {
    try {
      data = JSON.parse(dataLines.join("\n")) as Record<string, unknown>;
    } catch {
      data = { raw: dataLines.join("\n") };
    }
  }
  return { id, event, data };
}

function hostOf(addr: string): string {
  const idx = addr.lastIndexOf(":");
  return idx > 0 ? addr.slice(0, idx) : addr;
}

function portOf(addr: string): number {
  const idx = addr.lastIndexOf(":");
  const port = idx > 0 ? Number.parseInt(addr.slice(idx + 1), 10) : NaN;
  return Number.isFinite(port) ? port : 80;
}

function collectBody(res: http.IncomingMessage, done: (status: number, body: string) => void): void {
  let body = "";
  res.setEncoding("utf8");
  res.on("data", (chunk: string) => {
    body += chunk;
  });
  res.on("end", () => done(res.statusCode ?? 0, body));
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function asError(err: unknown): Error {
  return err instanceof Error ? err : new Error(String(err));
}
