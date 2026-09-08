# 03/04 — Baseline Audit and Entrypoint Map (Evidence)

Status: **PASS** (audit complete; defects recorded below)

## Frozen baseline
- Baseline SHA: `2c4850a7323071e539b59228ff2ef75e1a00970e`
- Store schema: **80** · Constitution: **1.0.0** · Runtime spec: 1.5.0
- Go: go1.27.0 linux/amd64
- Working tree carries pre-existing uncommitted TUI work (34 files), verified
  green and left untouched, exactly as during Process 00.

## Entrypoint map (task 04)

```
cmd/marshal/main.go
  └─ cli.Run → cli.Execute (internal/cli/cli.go:91)
       ├─ len(args)==0  → app.Open(root); on success tui.NewWorkspace(...).Run
       │                  on ANY error → prints usage, exit 0
       ├─ "tui"         → c.tui → app.Open(root) → hard error
       ├─ "doctor"      → doctor.Check (does NOT require app.Open)
       ├─ "init"        → app.Bootstrap
       └─ others        → daemon client or app.Open
```

`app.OpenWithOptions` (`internal/app/runtime.go:206`) is the single startup
funnel. It returns a hard error at **six** points before any control surface
can exist:

| # | Failure | Line | Recoverable? |
|---|---|---|---|
| 1 | `project.Discover` — needs a Git repo, a branch and a HEAD commit | 207 | **Yes** — no project is a normal state |
| 2 | `store.OpenWithObservability` — cannot create/open SQLite | 215 | Sometimes |
| 3 | `database.Migrate` | 219 | Sometimes |
| 4 | `database.Project` — "runtime is not initialized" | 224 | **Yes** — first run is normal |
| 5 | repository identity mismatch | 229 | **Yes** — moved project is normal |
| 6 | `policy.Load(CAPABILITIES.yaml)` | 233 | Sometimes |

`project.Discover` (`internal/project/layout.go:117`) additionally fails on a
Git repo with **no commits**, because it requires `symbolic-ref HEAD` and
`rev-parse HEAD` to succeed.

## Observed defects (reproduced against the built binary)

Binary built from baseline; commands run in throwaway directories.

### P0-01 — No control center without a Git repository
```
$ cd $(mktemp -d) && marshal
Usage: marshal [--json] <command> [arguments]        # exit 0
```
The control center never opens. The user is shown a usage screen, with no
indication that anything failed or what to do. This is the exact inversion of
the pack's primary invariant.

### P0-02 — `marshal tui` terminates with a raw Git error
```
$ cd $(mktemp -d) && marshal tui
open runtime for TUI: discover repository root: git [rev-parse --show-toplevel]:
exit status 128: fatal: not a git repository (or any parent up to mount point /)
Stopping at filesystem boundary (GIT_DISCOVERY_ACROSS_FILESYSTEM not set).
```
Raw subprocess stderr reaches the user; no Setup, Doctor, Help or Recent.

### P0-03 — Git repo without `marshal init` dies on a SQLite pragma
```
$ cd $(mktemp -d) && git init -q . && git commit --allow-empty -m seed && marshal tui
open runtime for TUI: configure SQLite with "PRAGMA foreign_keys = ON":
unable to open database file (14)
```
First run — the most common state a new user is in — produces a SQLite driver
error string. There is no first-run flow.

### P1-04 — Empty `marshal` invocation is silent about failure
The no-arg path discards the error entirely (`if err == nil && rt != nil`) and
falls through to usage, so even an operator cannot tell what went wrong.

## What already works and must be preserved

| Capability | Location | Note |
|---|---|---|
| `doctor` without a runtime | `internal/doctor/doctor.go` | Correctly independent of `app.Open`; already reports PASS/FAIL per check with a verdict |
| `init` bootstrap | `app.Bootstrap` | Creates layout; must stay explicit, never automatic |
| Startup reconciliation | `Runtime.ReconcileStartup` (runtime.go:462) | Already invoked at open; already tolerant (`_ =`) |
| Constitutional authority | `app.ConstitutionService` | Process 00; Process 01 must remain subordinate to it |
| Harness governance | `harness.AssessGovernance` | 4 honest states, added in Process 00 |
| Schema migration | `store.Migrate` | Sequential, transactional, idempotent |

## Design conclusion

The fix is **not** to make `app.Open` lenient — it is the execution runtime and
its strictness is correct. The fix is to introduce a startup assessment that
runs *before* and *independently of* `app.Open`, classifies what is wrong, and
opens the control center in a degraded mode when the failure is recoverable.
Execution capability stays gated exactly where it is today.

## Process 00 hook

Process 01 is subordinate. Startup may diagnose, explain and recommend; it may
not mark Ready, authorize repairs, enable ULTRA, weaken sandbox/network policy
or declare provider qualification. Those remain constitutional decisions.
