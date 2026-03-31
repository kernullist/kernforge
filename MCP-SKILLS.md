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

