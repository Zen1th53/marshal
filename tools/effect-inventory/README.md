# Material effect inventory

Run from the repository root, without executing any inventoried operation:

```sh
go run ./tools/effect-inventory
go test -v ./tools/effect-inventory
```

`sites.csv` is the explicit review table. Every discovered candidate has one
row: file, function (including receiver), effect kind, ordinal within that kind,
source hash, scope, guard, and review rationale. `source_hash` covers the
formatted containing function or package-level value declaration. Adding a
site, changing its containing code, deleting a site, duplicating an annotation,
or using an unknown guard fails the check. `-list` prints candidates for manual
review; it does not update the table. Never accept its default `none` rows as a
substitute for reviewing the code.

Material sites are source call sites for Git mutations, material external program and
agent launches, state restore/rollback, memory/evidence/approval/plan writes,
and listeners. Tmux pane stops are material dispatch sites, including explicit
Marshal provider switches and terminal cleanup; an operator command alone is
not a constitutional guard. The AST scan includes command construction, hostgit and Git
helpers, tmux dispatch, agent Launch, adapter process runners, listener setup,
restore methods, filesystem mutations and store SQL execution. Imports may be
aliased. Package-level closures and nested closures are included. Build-tagged
production files are scanned too, so platform-specific paths remain visible.

The detector identifies literal read-only Git commands (`rev-parse`, `status`,
`diff`, `log`, `show`, `cat-file`, `merge-base`, `rev-list`, `ls-files`,
`show-ref`, `worktree list`, `remote get-url`, query-only `config` and
`symbolic-ref`, and `hash-object` without `-w`). Local literal slices and
query-only loop variants are resolved; unknown command prefixes remain material.
Pure `hostgit.Root` path resolution, version/help/doctor probes, session
status/history queries, module metadata queries and reviewed private diagnostic
dispatchers are support candidates. They remain in the table for review, and
validation refuses to count them as material or guarded. General command
helpers remain material because they can dispatch mutations. A canary rollback
that appends a durable status record is a material record-write dispatch, not a
state restore.

Writes are found broadly and then reviewed: configuration files, temporary
probe files and socket cleanup are marked `support` with a rationale and are
excluded from both percentages. Task/goal/plan records and retained run,
verification, audit, memory, shared-channel and benchmark records count as
plans or operational evidence. A generic filesystem or SQL helper capable of
writing material records stays material even if some callers only write
configuration. Tests, test support, testdata, development tools, hidden state
folders are excluded. Schema migration calls remain in the table as support
sites, so adding a material write there still requires review. The inventory covers Go
runtime code, not installer shell operations or dependency implementations.

A wrapper dispatch and the lower-level effect boundary are distinct source
sites. Each call expression counts once, regardless of loop iterations or
number of runtime invocations. These are code-site coverage numbers, not the
percentage of user actions, test coverage, or a probability of safety.

## Guard review

- `gate`: a constitutional verdict can refuse this effect before it happens.
- `approval-binding`: the effect is refused unless its reviewed plan or
  confirmed snapshot binding still matches.
- `policy-authorisation`: an authority, role or capability decision must allow
  the effect before it happens.
- `sandbox`: the dispatched effect runs through enforced confinement.
- `none`: no mandatory guard of these kinds is established at this site.

When several guards apply, record one; prefer `gate` so constitutional coverage
is not lost. Merely recording a verdict is not enforcement. A named entry gate
that only checks the approved plan is `approval-binding`, not `gate`. An exact operator-confirmed backup digest is an approval binding only when
the restore refuses a mismatch before replacement and restores the verified
private snapshot. Generic backup integrity checks alone do not qualify. Input
validation, CAS, checksums, secret sanitisation, localhost binding, safer Git
flags and advisory scope checks are not any of the four coverage guards.

The table conservatively leaves general launch/store helpers `none` when they
also accept unguarded paths. It credits an indirect guard only where the
rationale identifies the production call path. For example, the target update
in Marshal close follows a permitting constitutional verdict; the checkpoint
ref written earlier does not. Evidence state transition follows the mandatory
policy authorizer, while generic evidence insertion does not.

The annotation table is a source review, not an automatic proof of reachability
or dominance. Changes to a guard's caller or composition need review even if the
leaf function's hash does not change. Reflection, function values supplied by
other packages and new effect APIs need detector review; a static scan cannot
prove arbitrary dynamically supplied code safe.

Both percentages use the same material-site denominator. `gate` contributes to
both numerators; any other guard contributes only to protection. Integer
arithmetic truncates to one decimal before formatting (2/3 prints 66.6%). The
release inventory test requires both notes to contain the exact printed lines.
