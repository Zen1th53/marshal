# Adapter Contract

## 1. Required Operations

Every adapter must expose or emulate:

```text
probe()
bootstrap(project)
run(task, mode)
status(session)
resume(session)
capabilities()
collect_evidence()
shutdown(session)
```

Optional:

```text
mcp_attach()
hook_install()
server_start()
subagent_spawn()
```

## 2. Capability Status

Allowed:

```text
native
emulated
probe_required
unsupported
```

Never convert `probe_required` to `native` from memory alone.

## 3. Bootstrap

Native instruction files should remain short and point to `AGENT-BOOTSTRAP.md`.

Do not copy 80+ protocol files into a native system prompt.

## 4. Structured Execution Envelope

Adapter output should normalize into:

```json
{
  "adapter": "gemini",
  "session_id": "...",
  "status": "success|failure|blocked|approval_required",
  "final_text": "...",
  "events": [],
  "tool_calls": [],
  "usage": {},
  "exit_code": 0,
  "raw_output_ref": null
}
```

Native output may be richer.

## 5. Permission Mapping

Map MARSHAL semantics rather than command spelling.

```text
read
write_worktree
shell
network
mcp_tool
secret_access
external_upload
git_commit
git_push
history_rewrite
deploy
destructive_operation
```

## 6. Instruction Trust

Native instruction-loading features do not change `protocols/INSTRUCTION-TRUST.md`.

A remotely loaded "instruction file" is still untrusted external content unless
project policy explicitly promotes it.

## 7. Session Semantics

Adapter must distinguish:

- native persistent session,
- native resume,
- runtime-managed continuity,
- unsupported continuity.

Do not fake a resumed session by merely replaying a summary without marking it.

## 8. Probe

A probe should be:

- read-only,
- bounded,
- version-reporting,
- safe with no credentials where possible.

The conformance harness records the probe result instead of assuming compatibility.
