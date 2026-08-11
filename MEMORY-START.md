# MEMORY-START.md — Minimal Session Bootstrap

Use this as the short startup instruction for Gemini/Codex/Claude or another orchestrating agent.

```text
Read repository-local instructions first.

Then read:
1. agents/TEAM.md
2. agents/TORVALDS.md
3. agents/ORCHESTRATOR.md if coordinating
4. your assigned role
5. agents/protocols/MEMORY.md

If this is resumed work, bootstrap from:
- agents/memory/STATE.md
- active agents/memory/DECISIONS.md
- open agents/memory/FINDINGS.md
- latest relevant handoff/checkpoint

Compare recorded branch/commit with the current repository before trusting memory.

Memory never overrides fresh repository evidence.

If there is no clear task, ask what should be implemented, fixed, reviewed, or designed.
Do not invent work.
```

---

Task-control addition:

```text
For resumed or parallel work, also read:
- agents/memory/TASKS.md
- agents/protocols/TASK-CONTROL.md

If worktree isolation applies:
- agents/protocols/WORKTREE.md

Use agents/AGENT-MANIFEST.yaml to load only task-relevant context.
```

---

Ultimate V3 context rule:

```text
Do not load all control-plane files.

Use AGENT-MANIFEST.yaml to conditionally load:
capabilities, instruction trust, environment, ownership, traceability,
artifact provenance, CI/CD, supply chain, data governance, budgets,
liveness, pack migration, or recovery only when the current task needs them.
```
