# Runtime memory fabric Phase 2 validation

## Starting state

- Starting branch: `main`
- Starting HEAD: `029673c45101783b114eadf4cd2131195eb75a50`
- Starting worktree: clean
- Implementation branch: `feat/memory-fabric-phase2`
- Host: Linux 7.0.11-arch1-1 amd64, Intel Core Ultra 7 255H, 16 CPUs
- Toolchain: Go 1.26.4-X:nodwarf5
- Canonical schema after this change: 72

The Phase 1 architecture was already real: `Runtime` owned the production
`MemoryService`, SQLite `memory_records_v2` was canonical, task-start recall
and completion capture were automatic, writes were candidate-first, task slots
used CAS, and Runtime/MCP/A2A/Web used canonical services. Phase 2 did not
replace those paths.

## Changes and measured evidence

### Retrieval quality

The versioned corpus at
`internal/memory/eval/testdata/golden_relevance_v1.json` contains 21 records
and 12 queries covering exact and paraphrased architecture facts, procedures,
failure signatures, paths, symbols, stale and superseded facts, task/private
scope, worktree-local state, conflicts, and graph relations. Each query declares
required, acceptable, irrelevant, and forbidden records.

Command:

```text
go test -count=1 -run '^TestMemoryRecallGoldenQuality$' -v ./internal/app
```

Result:

```text
Recall@1/3/5/10             1.0000 / 1.0000 / 1.0000 / 1.0000
Precision@1/3/5/10          1.0000 / 0.3333 / 0.2000 / 0.1000
MRR                         1.0000
NDCG@1/3/5/10               1.0000 / 1.0000 / 1.0000 / 1.0000
false-positive recall rate  0.0769
stale exposure              0
unauthorized exposure       0
tombstone exposure          0
superseded exposure         0
conflicted exposure         0
context bytes/useful        398.92
estimated tokens/useful     100
mean first-useful bound     436.43 microseconds
```

The corpus is intentionally small enough for normal CI. These figures describe
this checked-in corpus, not universal retrieval quality.

Recall now obtains a canonical authorized-ID set before inspecting derived
content, bounds canonical reloads to 256 candidates, bounds persisted receipt
decisions to 256, and records how many visible decisions were omitted. A cold
projection has a bounded, SQL-authorized exact-phrase fallback. Exact title
matches may seed graph traversal; weak token matches may not.

### Governed learning

`MemoryService.ProposeConsolidation` produces procedure, anti-pattern, and
verified-fact candidates from two or more same-scope canonical source records.
It preserves contributing IDs/evidence, never widens scope, never raises model
authority, rejects tombstoned or secret-bearing input, uses deterministic IDs,
and represents disagreement as `conflicted`. Run completion invokes the
bounded scheduler after a matching second episode. Promotion remains a
separate governed action.

### Live task memory

Schema 72 adds a bounded canonical task-change cursor. Events contain
notification/reference metadata only; consumers reload `memory_records_v2`.
Reads authorize the caller before returning head, counts, or event metadata.
Private writes do not advance the shared cursor. Revocation is checked on every
refresh. The window retains 4,096 events and returns an explicit expired-cursor
error rather than silently skipping changes.

Live shared memory means that an active agent sees an authorized peer update at
the next tool-call, agent-turn, orchestrator-yield, explicit-refresh, or provider
continuation boundary. MARSHAL does not mutate an opaque provider token stream.

Parallel scale command:

```text
MARSHAL_TEST_MEMORY_PARALLEL_SCALE=1 go test -count=1 -run '^TestTaskMemoryParallelScale$' -v ./internal/app
```

```text
agents  write p50   write p95   write p99   refresh p50  refresh p95  refresh p99  throughput
2       1.449 ms    1.449 ms    1.449 ms    0.409 ms     0.409 ms     0.409 ms     1007.9/s
5       3.493 ms    4.375 ms    4.375 ms    1.156 ms     1.314 ms     1.314 ms      959.4/s
10     11.211 ms   15.021 ms   15.021 ms    1.096 ms     1.795 ms     1.795 ms      644.7/s
20     16.063 ms   27.167 ms   27.167 ms    2.133 ms     6.235 ms     6.235 ms      692.1/s
50     36.354 ms   63.980 ms   65.927 ms    6.368 ms     7.602 ms     8.601 ms      751.1/s
```

All levels reported missed updates `0` and duplicate deliveries `0`.

The opt-in real-provider test started two independent authenticated Codex CLI
processes concurrently. Their execution intervals overlapped. Each live agent
workflow published through normal `MemoryService` task APIs, waited at an
explicit turn boundary, then both were released to refresh canonical state and
observed the other's finding. A same-revision concurrent write produced exactly
one winner and one CAS conflict. Command and final result:

```text
MARSHAL_TEST_REAL_PARALLEL_CODEX_AGENTS=1 \
  go test -count=1 -run '^TestRealParallelProviderAgentsSharedMemory$' \
  -v ./internal/integration
PASS (48.40 seconds)
```

Codex + Claude was attempted but Claude Code reported that it was not logged
in. It is therefore `NOT_RUN`, not PASS. Gemini and OpenCode CLIs were absent;
Ollama was installed but its local daemon/model service was unavailable.

### Repository and worktree truth

Runtime fingerprints now distinguish canonical HEAD, worktree HEAD, worktree
identity, branch, provider/model/agent and risk. Unmerged facts remain
worktree-local candidates. A caller in another worktree receives a safe
`WORKTREE_MISMATCH` exclusion; canonical repository evidence still wins.

### Session imports

The importer registry now includes the verified Gemini CLI JSONL message
format in addition to Codex and Claude. It accepts public user/model content,
excludes thoughts and tool-call internals, applies the deterministic firewall,
and remains candidate-only and idempotent. Current OpenCode stores histories
in SQLite; no fragile guessed JSON adapter or filesystem path was added.
Automatic discovery/checkpointing remains open.

### Scale

Final 100k command:

```text
MARSHAL_TEST_MEMORY_100K=1 go test -count=1 \
  -run '^TestMemoryScale100K$' -v ./internal/app
```

Result:

```text
records                    100,000
seed time                  39.456887005 s (2,534.4 records/s)
DB after seed              127,594,496 bytes
DB after 31 recalls        127,692,800 bytes
derived rebuild            2.702653499 s
cold recall                163.445080 ms
recall p50                 113.618926 ms
recall p95                 160.584879 ms
recall p99                 163.445080 ms
heap delta after seed      1,559,456 bytes
heap delta after indexes   246,005,152 bytes
```

The lexical/graph accelerator is in-process, so there is no separate derived
index file size. Cold/warm process-start timing, RSS and CPU sampling were not
measured and remain `NOT_RUN` rather than inferred from heap deltas.

### Controlled value regressions

Command:

```text
go test -count=1 \
  -run '^(TestSharedMemoryReducesDuplicateDiscovery|TestFailureRecallReducesRepeatedAttempts)$' \
  -v ./internal/app
```

The controlled shared-memory case reduced duplicate discovery operations from
2 to 1 (50%). The controlled failure-recall case reduced attempts from 2 to 1
(50%). Both use the canonical service and real SQLite task/recall paths. They
are deterministic engineering regressions, not statistically powered claims
about provider time, tokens, or task success; those remain `NOT_RUN`.

## Security and recovery coverage

Passing tests cover ACL-before-ranking, private/shared isolation, grant
revocation, event inference resistance, secret rejection, prompt armor,
candidate-only provider writes, consolidation poisoning, staleness,
worktree mismatch, tombstones after rebuild/restart, CAS conflicts, runtime
restart, bounded cursors, cursor expiry, duplicate import, and canonical reload.

Notification events are not canonical truth. Losing a derived index or event
consumer state cannot delete canonical memory.

## Honest limitations

- Real cross-provider Codex-to-Claude/Gemini/OpenCode/Ollama execution is
  `NOT_RUN` because authenticated providers were not simultaneously available.
- Task-success uplift and shared-memory value have deterministic regression
  coverage, but no statistically powered paid-provider study was run.
- 250k, 500k, 1M and long-run soak profiles are `NOT_RUN`.
- The 100k in-process lexical projection uses substantial heap; it remains a
  disposable accelerator and needs further memory-density work.
- Automatic importer discovery/checkpointing and OpenCode SQLite ingestion are
  future work.
- Web exposes existing canonical memory views and truthful doctor probes, but
  a dedicated historical quality-series dashboard is not implemented.
- The optional vector channel still requires a real local embedding backend;
  MARSHAL does not fake vector recall with empty embeddings.
