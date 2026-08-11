# MEMORY.md — Shared Team Memory Protocol

## 0. Mission

Give every role enough continuity to resume work without allowing stale memory to override reality.

---

## 1. Startup Order

For resumed non-trivial work:

```text
repository/local policy
→ TEAM.md
→ TORVALDS.md
→ ORCHESTRATOR.md when coordinating
→ assigned role
→ this protocol
→ memory/STATE.md
→ active memory/DECISIONS.md
→ open memory/FINDINGS.md
→ latest relevant handoff/checkpoint
→ historical/semantic recall only if needed
```

Progressive disclosure is mandatory.

Do not dump the full memory corpus into every prompt.

---

## 2. Bootstrap

At session start:

1. read current repository branch/worktree/HEAD,
2. read `STATE.md`,
3. compare recorded repository identity with current repository,
4. mark stale mismatches,
5. read active decisions,
6. read open findings,
7. read latest relevant handoff,
8. retrieve history only for the actual current problem.

Then the agent should know:

```text
task
phase
last owner
open blockers
open findings
relevant decisions
repository mismatch
next action
```

---

## 3. Precedence

```text
fresh repository/runtime evidence
> explicit current user/task requirement
> approved spec / ADR
> STATE
> active DECISIONS
> durable MEMORY
> historical retrieval
> model recollection
```

Never silently merge a conflict between levels.

---

## 4. What to Read

### Always for resumed work

- `STATE.md`
- active relevant `DECISIONS.md`
- open `FINDINGS.md`

### When ownership/session changed

- latest relevant `HANDOFFS.md`
- latest checkpoint

### Only when useful

- historical agent sessions,
- semantic memory,
- old bug investigations,
- rejected designs,
- prior operational friction.

Historical memory is not a ritual.

---

## 5. What to Write

### STATE

Update when:

- task/phase changes,
- role starts/stops,
- blocker appears/resolves,
- next action changes,
- branch/commit changes,
- fresh verification snapshot exists.

### DECISION

Record when a durable choice is accepted by the correct authority.

### FINDING

Record reproducible unresolved QA/AppSec defects.

### HANDOFF

Record when ownership moves.

### CHECKPOINT

Record before context/risk boundaries where resume value is high.

### MEMORY

Promote only durable verified facts/lessons.

---

## 6. Authority

### Orchestrator

Owns:
- task,
- phase,
- overall coordination state,
- next action.

### Architect

Owns:
- architecture decisions,
- architecture invariants.

### Developer

Owns:
- implementation state,
- branch/commit pointer,
- developer verification snapshot.

### QA

Owns:
- QA findings,
- QA verdict.

### AppSec

Owns:
- security findings,
- security gate.

No role edits another role's verdict/finding simply to make the task appear complete.

---

## 7. Handoff

Before ownership moves:

1. update STATE,
2. write concise handoff,
3. link decisions/findings,
4. record exact repository commit,
5. record verification performed,
6. record verification not performed,
7. state next action.

Receiver must compare handoff commit with current HEAD.

Do not blindly trust the sender's success claim.

---

## 8. Atomic Writes

Automated file-backed memory writes should use:

```text
read current revision/hash
→ build complete sibling temporary file
→ flush
→ atomic rename
```

Use locking or compare-and-swap for concurrent writers.

Do not perform expensive LLM summarization while holding a write lock.

---

## 9. Conflict

Conflict handling:

```text
detect
→ preserve both
→ mark conflict
→ inspect provenance/current evidence
→ route to authority
→ explicitly supersede/reject
```

Never use last-write-wins for architectural/security truth.

---

## 10. Staleness

Evaluate retrieved memory against:

- current commit,
- referenced files,
- dependency versions,
- active task,
- decision lifecycle status.

Allowed result:

```text
staleness: unknown
```

Do not say "unchanged" without checking.

---

## 11. Consolidation

Memory should become smaller and more useful:

```text
raw/session history
→ episodic summary
→ handoff/checkpoint
→ durable fact/decision/lesson
```

Do not keep duplicate summaries forever.

Do not rewrite authoritative decisions merely to compress them.

---

## 12. Promotion Gate

Historical memory may become durable only if:

```text
[ ] relevant
[ ] provenance available
[ ] not contradicted by current repository
[ ] verified/accepted
[ ] future sessions likely benefit
```

Rejected/superseded approaches may be worth retaining when rediscovery is expensive.

---

## 13. Secret Safety

Never store/index:

- passwords,
- API keys,
- bearer tokens,
- session cookies,
- private keys,
- recovery phrases,
- production secrets.

History indexers should redact secrets before indexing.

Memory is not a secret manager.

---

## 14. Semantic Retrieval

Optional pipeline:

```text
project/task/ACL filter
→ lexical retrieval
→ semantic rerank
→ provenance + staleness validation
→ compact return
```

TurboVec may implement the semantic-index layer.

It is never authoritative by itself.

---

## 15. Graph Retrieval

Optional graph layer may relate:

```text
task
→ decision
→ component
→ file
→ finding
→ test
→ commit
```

Graph nodes/edges must resolve back to canonical records/evidence.

Cognee may implement this layer.

---

## 16. Historical Session Retrieval

A Deja Vu-style adapter may answer:

- did another agent debug this before?
- which files were touched?
- which command proved the previous hypothesis?
- was this design previously rejected?
- what did the last agent hand off?

Promote only verified useful conclusions.

Do not import the entire transcript into durable memory.

---

## 17. Versioning / Rollback

Memory versioning may later support:

```text
snapshot
branch
merge
rollback
audit
```

Memoria is a useful reference.

Important:

```text
memory rollback != code rollback
```

Never silently revert repository code when reverting a memory checkpoint.

---

## 18. Multi-Machine / Multi-Agent Sync

Sync durable memory with:

- stable IDs,
- provenance,
- revisions,
- conflict detection.

Do not sync:

- locks,
- temporary files,
- local process state,
- raw secrets.

Refuse automatic conflicting-decision merge.

---

## 19. Branch / Worktree Scope

Working memory must record:

```text
project
branch
worktree
commit
```

Feature-branch state must not masquerade as main-branch truth.

Durable architecture decisions may be project-wide when explicitly marked.

---

## 20. Future MCP Boundary

Recommended stable tools:

```text
team_memory_status
team_memory_recall
team_memory_remember
team_memory_checkpoint
team_memory_handoff
team_memory_decision
team_memory_finding
team_memory_audit
```

The MCP service must enforce role write authority.

---

## 21. Backend Failure

If optional semantic/graph/history backend is unavailable:

- repository work may continue,
- compact file memory still works,
- history-dependent claims become UNVERIFIED.

If canonical current state is corrupt:

- fail loudly,
- recover from git/checkpoint,
- do not fabricate state.

---

## 22. Task Completion Memory Gate

Before final ownership release:

```text
[ ] STATE current
[ ] active decisions current
[ ] findings current
[ ] handoff written if ownership moves
[ ] durable lessons promoted only if valuable
[ ] verification snapshot records commit
[ ] stale records are not presented as current
```

---

## Task Graph Integration

`memory/STATE.md` is the compact current view.

`memory/TASKS.md` is the dependency/ownership graph.

When they disagree:
- inspect repository evidence,
- inspect latest handoff/checkpoint,
- repair stale coordination state.

Do not infer task ownership from prose alone when a canonical task record exists.
