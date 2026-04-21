# MCP And Skills

`kernforge` supports local skills and stdio-based MCP servers.

## Workspace Config

Create a workspace config with:

```text
/init config
```

Example `.kernforge/config.json`:

```json
{
  "skill_paths": ["./.kernforge/skills"],
  "enabled_skills": ["checks"],
  "mcp_servers": [
    {
      "name": "example",
      "command": "node",
      "args": ["path/to/server.js"],
      "cwd": ".",
      "disabled": true
    },
    {
      "name": "web-research",
      "command": "node",
      "args": [".kernforge/mcp/web-research-mcp.js"],
      "env": {
        "TAVILY_API_KEY": "",
        "BRAVE_SEARCH_API_KEY": "",
        "SERPAPI_API_KEY": ""
      },
      "cwd": ".",
      "capabilities": ["web_search", "web_fetch"]
    }
  ]
}
```

## Skills

Create a starter skill with:

```text
/init skill checks
```

Expected layout:

```text
.kernforge/
  skills/
    checks/
      SKILL.md
```

Prompt usage:

- Use `$checks` to activate a skill for the current request.
- Use `/skills` to inspect discovered skills.
- Use `/reload` after editing skill files or config.

## MCP

Configured MCP servers are started over stdio and their tools are exposed to the model.

Useful commands:

- `/mcp`
- `/resources`
- `/resource <server:uri-or-name>`
- `/prompts`
- `/prompt <server:name> {"arg":"value"}`

Prompt usage:

- Use `@mcp:server:resource` to inject a listed MCP resource into the prompt context.
- Remote MCP tools are exposed as `mcp__server__tool`.
- Resource readers are exposed as `mcp__resource__server`.
- Prompt resolvers are exposed as `mcp__prompt__server`.

### Web Research MCP Setup

Use capability tags when you connect a live web-search or browser MCP:

- On startup, Kernforge deploys the bundled web-research MCP to `~/.kernforge/mcp/web-research-mcp.js`.
- If `~/.kernforge/config.json` does not already contain an equivalent web-research MCP entry, Kernforge auto-adds one.
- This workspace also includes `.kernforge/mcp/web-research-mcp.js` and `.kernforge/config.json`.
- Keep `capabilities` such as `"web_search"` and `"web_fetch"` on the server entry.
- You can provide `TAVILY_API_KEY`, `BRAVE_SEARCH_API_KEY`, or `SERPAPI_API_KEY` either through your shell environment or through `mcp_servers[].env` in config.
- `fetch_url` uses Jina Reader first, then falls back to a direct fetch. Set `WEB_RESEARCH_DISABLE_JINA=1` if you want to skip the reader path.
- Run `/reload` after editing `.kernforge/config.json`.
- Run `/mcp` and confirm the server exposes tools such as `mcp__web_research__search_web` or `mcp__web_research__fetch_url`.
- In PowerShell, a quick session setup looks like:

```powershell
$env:TAVILY_API_KEY = "replace-me"
```

- Test it with a prompt like `Hypervisor-based anti-cheat detection 최신 기법을 조사해`.

When a web-research MCP is available, Kernforge will prefer those tools before local file inspection for latest/current research requests.

## Reloading

Use:

```text
/reload
```

This reloads:

- config from disk
- memory files
- discovered skills
- MCP server connections

