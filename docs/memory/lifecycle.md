# MARSHAL Memory Lifecycle and Promotion State Machine

**Specification:** T82 — Memory Lifecycle & Promotion State Machine

## 1. Lifecycle States

Canonical memory records progress through defined lifecycle states:

```text
observed
  ↓
candidate
  ↓
verified
  ↓
durable
```

### Exceptional & Terminal Paths

```text
observed/candidate/verified → rejected
durable → stale (requires re-verification to become durable)
durable → conflicted
durable → superseded
any → tombstoned (terminal state)
```

## 2. Promotion Authority Matrix

| Target State | Memory Kind | Minimum Authority Required |
|---|---|---|
| `candidate` | All | `agent` |
| `verified` | All | `verified` (multi-agent consensus or test evidence) |
| `durable` | `working`, `semantic`, `episodic`, `procedural`, `handoff`, `checkpoint` | `verified` |
| `durable` | `decision`, `finding`, `failure` (Protected Classes) | `policy` or `operator` |

## 3. Invariants

1. **No Self-Promotion**: A single untrusted agent cannot promote its own candidate assertion directly to `durable`.
2. **Re-Verification on Stale**: Records marked `stale` cannot jump back to `durable` without re-entering `candidate` / `verified`.
3. **Terminal Tombstone**: Tombstoned records cannot be reactivated.
