# KernForge VS Code Extension (Slice-1 scaffold)

This is a **Slice-1 scaffold** for a KernForge IDE integration. It is intentionally
small and is **not** wired into the Go build (`go build ./cmd/kernforge` never
touches this directory). It compiles independently with the TypeScript toolchain.

## What this slice delivers

- A `DaemonClient` (`src/daemonClient.ts`) that:
  - locates the `kernforge` binary (config override, then `PATH`, then common
    build-output locations),
  - reads the daemon state file the Go daemon writes
    (`<home>/.kernforge/daemon/daemon.json`),
  - probes `GET /health` and (optionally) auto-starts the daemon,
  - opens the token-authed `GET /stream` Server-Sent Events channel and decodes
    observe-only RPC progress events,
  - posts `tools/call` requests to `POST /rpc`.
- Command registrations: **KernForge: Analyze Selection**, **KernForge: Review**,
  **KernForge: Connect To Daemon**, **KernForge: Disconnect From Daemon**.
- A `PreviewWebView` (`src/previewWebView.ts`) that intercepts EditPreview-shaped
  tool responses (unified-diff text) and offers **Apply** / **Cancel**.

## Safety model (important)

The extension is a **read-only observer and a thin UI**. It has no edit authority:

- The `GET /stream` channel is observe-only. It mirrors RPC progress that already
  happened; it cannot mutate the workspace and cannot drive the agent loop.
- **Apply** does **not** write files. It issues a normal `tools/call` back through
  the daemon `/rpc` endpoint. The daemon runs that call through the agent loop's
  permission / review / edit gates, exactly as a CLI invocation would. The
  extension can never bypass a gate.
- Every daemon request carries the daemon token. The stream endpoint rejects an
  unauthenticated or wrong-token connection with `401`.

## Build

```sh
cd cmd/kernforge-vscode-extension
npm install
npm run compile      # tsc -p ./  -> out/extension.js
```

For iterative development:

```sh
npm run watch
```

Then press `F5` in VS Code (with this folder open) to launch an Extension
Development Host.

> Note: `npm install` / `tsc` are **not** run as part of the KernForge Go build or
> CI for this scaffold slice. The TypeScript is written to be coherent and
> well-typed by construction; running `tsc` requires `@types/vscode` and
> `@types/node` from `npm install`.

## Remaining (out of scope for this slice)

- Rich status/progress view backed by the stream (instead of the OutputChannel).
- A code-lens / inline action surface for Analyze and Review.
- An artifact browser over the daemon's analysis/evidence resources.
- A JetBrains plugin counterpart.
- Packaging (`vsce package`) and a publisher pipeline.
- Last-Event-ID stream resume and reconnect/backoff.
