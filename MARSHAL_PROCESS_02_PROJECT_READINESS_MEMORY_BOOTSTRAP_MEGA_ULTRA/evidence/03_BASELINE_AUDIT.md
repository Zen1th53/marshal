# 03/04 — Process 02 Baseline Audit and Entrypoint Map

Status: **PASS** (audit complete; defects recorded and reproduced)

## Frozen baseline
- Baseline SHA: `71ac697e93860d9f0e3eeb0d1294dadf3715f3ef`
- Store schema: **80** · Constitution: **1.0.0** · Runtime spec: 1.5.0
- Go: go1.27.0 linux/amd64
- Pre-existing uncommitted TUI work (34 files) present since Process 00, green,
  left untouched.

## Project entrypoint map (task 04)

```
project.Discover(root)                      internal/project/layout.go:117
  ├─ git rev-parse --show-toplevel          → repository root (requires Git)
  ├─ filepath.EvalSymlinks                  → canonical root
  ├─ git symbolic-ref HEAD                  → branch (fails on detached HEAD)
  └─ git rev-parse HEAD                     → HEAD (fails with no commits)

app.Bootstrap(root)                         internal/app/runtime.go:167
  └─ store.InitProject{ID: "PROJECT-local", Repository: layout.Root}

app.OpenWithOptions(root)                   internal/app/runtime.go:206
  ├─ project.Discover
  ├─ store.Open + Migrate
  ├─ database.Project()                     → stored identity
  ├─ identity.Repository != layout.Root     → HARD CONFLICT   ← P0-02-01
  └─ policy.Load(CAPABILITIES.yaml)
```

## Identity model at baseline

`ProjectID` is the compile-time constant `localProjectID = "PROJECT-local"`
(`internal/app/runtime.go:47`), returned by `Runtime.ProjectID()` and used for
memory scoping, agent registration, session creation, artifacts and memory
recall (`runtime.go` lines 194, 325, 341, 558, 585, 814–815, 974, 983).

**Assessment:** this is *not* a live cross-project memory leak, because each
project keeps its own `.marshal/state.db` and the ID is only ever resolved
within one database. Every project calling itself `PROJECT-local` inside its
own store is consistent, if uninformative.

It is, however, the reason identity is **positional**: with no stable
per-project identifier, the only thing distinguishing one project from another
is its path — which is precisely what the pack forbids (`Path != ProjectID`).

## Defects reproduced

### P0-02-01 — A moved project cannot be opened

`internal/app/runtime.go:228` compares the stored repository path against the
discovered root and treats any difference as `ErrConflict`.

Reproduced by `TestMovedProjectIsRejectedAtBaseline`:

```
baseline defect confirmed: a moved project cannot be opened:
conflict: runtime repository identity differs
```

Moving a project is ordinary — reorganizing a workspace, renaming a parent
directory, restoring a backup elsewhere. None of it changes which project it
is. At baseline all of it makes the project permanently unopenable, with a
message naming an internal comparison.

### P0-02-02 — Startup reports a moved project as fully READY

Verified against the built binary. After moving an initialized project:

```
$ marshal setup
Readiness: LIMITED
  [READY] project.git            Git is available.
  [READY] project.initialized    This project is set up for MARSHAL.
  [READY] project.policy         The project's capability policy is present.
  [READY] project.repository     A Git repository was found.
```

Every project dimension reads READY while `app.Open` would reject the project
outright. This is a UI-versus-backend divergence of exactly the kind Process 00
Article XI forbids, and it was introduced by Process 01's assessment not
knowing about identity binding. Process 02 owns the fix.

### P1-02-03 — No stable identity to bind memory or evidence to

Because every project is `PROJECT-local`, nothing recorded in a project carries
an identifier that would survive being copied elsewhere. A `.marshal` directory
copied into a different repository would be accepted on its contents alone:
there is no binding to check it against.

### P1-02-04 — `project.Discover` fails on ordinary repository states

`symbolic-ref --quiet --short HEAD` fails on a detached HEAD, and
`rev-parse HEAD` fails in a repository with no commits. Both are legitimate
states. Process 01 already reports the empty-repository case honestly at the
control center; the runtime still cannot open such a project.

## Existing primitives to reuse (no shadow architecture)

| Concern | Existing | Verdict |
|---|---|---|
| Repository discovery | `internal/project.Discover` | EXTEND — add identity, keep behavior |
| Project record | `store.InitProject` / `store.Project` | EXTEND — add identity columns |
| Memory candidate pipeline | `app.MemoryService.ExtractCandidate` → `Promote` | REUSE — already evidence-gated |
| Memory promotion gate | `constitution.GatePromotion` | REUSE — Process 00, already refuses AI promotion |
| Instruction firewall | `harness.ScreenInstructions` | REUSE — Process 00 |
| Secret redaction | `internal/evidence` sanitizer, `secrets` broker | REUSE |
| Readiness model | `internal/startup` | EXTEND — add project identity checks |
| Cross-project isolation | `constitution.InvProjectIsolation` | REUSE |
| Scope violations | `startup.RuntimeState.ScopeViolations` | REUSE — feed real values |

## Design conclusion

Two changes carry Process 02:

1. **A stable ProjectIdentity**, persisted in the project's own store and in a
   project-local binding file, derived from durable repository evidence rather
   than from the path. A moved project keeps its identity; a *different*
   repository at a reused path does not inherit it.
2. **A readiness model that consults that identity**, so Setup cannot report
   READY for a project the runtime would refuse.

Everything else in the pack — scope, trust, memory assimilation — hangs off
having a trustworthy answer to "which project is this?".
