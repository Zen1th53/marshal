# Antigravity Adapter

## Native Surfaces

- CLI binary: `agy` (Google Antigravity CLI).
- IDE & Agent Runtime: Google Antigravity IDE / Custom Runtime.
- Customizations: Skills (`skills/<skill_name>/SKILL.md`), Rules (`rules/*.md`, `AGENTS.md`), Plugins (`plugins/<plugin_name>/`), MCP (`mcp_config.json`), Hooks (`hooks.json`).
- Non-interactive automation: `agy exec` / headless prompt mode.
- Model selection: Gemini 3.8 Flash (default reasoning model), Gemini 2.5 Pro, Gemini 2.5 Flash.
- Effort knobs: Thought intensity / reasoning effort (`high`, `medium`, `low`, `none`).
- Artifacts: Native artifact directory with markdown documents, diff blocks, mermaid diagrams, and structured summaries.
- Session / Resume: Native session persistence and subagent conversation resumption via conversation ID.

## Canonical Identity

- `harness`: `antigravity`
- `model`: `gemini-3.8-flash` (or selected Gemini model)
- `role`: Fixed MARSHAL role (`developer`, `architect`, `qa`, `appsec`)

## Headless Execution

For automated execution:
- Execute `agy` in the exact managed worktree directory.
- Capture session/conversation ID and structured outputs.
- Track resource usage (`TotalTokens`, `CostUSD`, `ModelCalls`) preserving unknown metrics as nil.

## Security

- MARSHAL policy strictly outranks native harness settings.
- Native approval bypass flags or dangerously open permissions are strictly prohibited under MARSHAL authority.
- Fail-closed security writes: write paths require MARSHAL authorization and valid capability grants.
- API keys, credentials, and authentication tokens are never leaked into logs, claims, or persistent transcripts.
