# Runtime memory fabric

MARSHAL v1.0.1 uses SQLite schema v72. `memory_records_v2` is the canonical
durable memory store; lexical, vector, graph, and cache structures are derived
projections.

## Runtime lifecycle

`app.Runtime` owns one `MemoryService`:

1. At task start, it builds a query from task and repository state.
2. Recall applies private-scope filtering, principal/task/project ACLs,
   lifecycle, expiry, repository freshness, and record/byte budgets.
3. The selected records are escaped and rendered as historical data, not
   instructions, in a provider-neutral context block.
4. A caller-bound retrieval receipt records included/excluded decisions,
   tracks, budgets, and resulting evidence links without leaking denied IDs.
5. At completion, deterministic run metadata and evidence references are
   captured as a task-scoped candidate. Raw hidden reasoning and provider
   transcripts are not automatically persisted.

Promotion remains governed. Candidate, verified, durable, superseded,
tombstoned, conflicted, expired, and rejected lifecycle states are enforced
before retrieval.

## Sharing and freshness

Project/task scopes permit authorized agents and providers to share canonical
memory across runtime restarts. Private/operator scopes remain isolated. Typed
handoffs use the same SQLite runtime rather than a provider-specific memory
database.

Schema v72 adds a durable, bounded task-memory change cursor. Consumers can
refresh shared task state without storing memory bodies in the event table. An
expired cursor forces a canonical reload, and authorization is checked before
cursor metadata is exposed.

Repository HEAD and worktree changes participate in freshness checks. Stale
records can be excluded with an explainable receipt instead of silently
influencing a task.

## Retrieval and consolidation

Canonical recall supports exact and lexical tracks, with optional derived
vector and graph tracks. Ranking is deterministic and bounded. Retrieval
quality/scale tests include fixture metrics and a scheduled 100k-record gate;
release notes distinguish executed gates from unrun external-provider tests.

Consolidation proposes governed candidates from repeated verified material. It
does not bypass authority, evidence, conflict, scope, or lifecycle rules.
Conflicting facts preserve provenance and require governed resolution.

## Session imports

Verified import adapters exist for supported provider/session formats,
including Codex, Claude, and Gemini paths represented in the current code.
Imports retain provenance and pass through the memory firewall. A parser being
implemented does not imply that every provider filesystem/session version was
validated in the v1.0.1 release environment.

## Security and custody

- direct-ID reads repeat scope and ACL checks;
- secret/high-entropy content is rejected or sanitized before persistence;
- context delimiters are escaped, but semantic prompt injection still requires
  policy and content-as-data handling;
- mutation envelopes, evidence IDs, authority, source, timestamps, conflicts,
  and supersession links are retained; and
- tombstones do not rewrite historical receipts or audit evidence.

Backups include the canonical SQLite state. Restore performs integrity,
project, and schema preflight checks and keeps a safety backup when replacing
an existing database.

## Known limits

- optional vector retrieval requires a configured local provider;
- federation and network memory sync are not Community v1.0.1 features;
- CLI/Web do not expose every lower-level handoff or consolidation operation;
  and
- authenticated external-provider E2E is NOT_RUN for v1.0.1 unless the release
  notes explicitly report otherwise.

See the [memory authority map](memory/current-state-audit.md).
