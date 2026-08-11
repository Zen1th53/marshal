# Project Initialization Protocol

## Mission

Install the Agent OS contract into a repository with the minimum local changes.

## Flow

```text
detect repository
→ detect native instruction files
→ detect stack/package manager
→ detect CI
→ detect CODEOWNERS/governance
→ detect available agent CLIs
→ choose adapter
→ create/merge small native bootstrap
→ initialize memory/task state
→ validate
```

## Existing Native Instructions

If the repo already has `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, or similar:

```text
do not overwrite
→ inspect
→ add the smallest compatible pointer to agents/AGENT-BOOTSTRAP.md
```

Project-specific rules remain project-specific.

## Initial Memory

Initialize only:

- repository identity,
- branch/HEAD,
- environment facts that were actually discovered,
- no active task unless one exists,
- no invented decisions/findings.

## Validation

Run:

```bash
python agents/conformance/runner.py validate-pack --root agents
python agents/tools/agentos.py detect-project .
```

Then run the selected adapter's read-only probe.
