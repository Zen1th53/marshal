# Crush Adapter

## Native Surfaces

Upstream design exposes:
- multiple context file names including `AGENTS.md`,
- sessions backed by SQLite,
- MCP,
- hooks before tool execution,
- permissions/allowed tools,
- skills,
- agent coordinator/subagents,
- internal pub/sub,
- file tracking.

## Recommended Bootstrap

Use `AGENTS.template.md`.

Crush native sessions and SQLite remain agent-native state; Agent OS canonical
task/decision/finding state remains separate unless a reconciliation adapter is
implemented.

## Hooks

Hooks are a useful enforcement point for Agent OS policy.

Do not assume hook order/shape without probing the installed release.

## Config Trust

Upstream Crush configuration can execute shell-style command substitutions.
Treat project configuration as privileged/trusted code only after review.

## Runtime

When a stable headless/server surface is available in the installed version,
prefer it over terminal scraping. Otherwise use an isolated wrapper and mark the
surface `probe_required`.
