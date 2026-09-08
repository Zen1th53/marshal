# Projects, identity and what MARSHAL remembers

This describes how MARSHAL decides which project it is looking at, what it is
allowed to change, and what it does with knowledge a project already carries.

## The rule

**MARSHAL may inspect any project, but it only works on one when every change
can be kept controlled, recoverable and auditable.**

Looking is cheap and safe. Changing things is not, and the difference between
the two is the whole of this process.

## Three things that are not the same

| | What it is |
|---|---|
| **Repository** | What Git tracks |
| **Project** | What MARSHAL has adopted an identity for |
| **Working scope** | What MARSHAL is allowed to modify |

They coincide in the ordinary case. They diverge in exactly the situations
that cause damage: a monorepo where work should touch one package, a checkout
containing a vendored repository whose history you do not control, a symlink
pointing somewhere else entirely.

## A path is not an identity

A project that moves is still the same project. A different repository that
later occupies the old path is not.

Both halves matter. Reorganizing a workspace, renaming a parent folder or
restoring a backup elsewhere are ordinary acts that must not destroy a
project's history. Inheriting a previous occupant's history would be a
cross-project leak with none of the usual warning signs.

So identity comes from durable repository evidence — the first commit in the
history, plus a value minted when MARSHAL first adopts the project — recorded
in a file that travels with the project. Not from where the directory sits.

### Why the minted value

Two projects scaffolded from the same template in the same second produce
byte-identical first commits, and therefore the same commit hash. Git is right
to call that one lineage. MARSHAL cannot afford to: if the two shared an
identity, each would see the other's memory.

Every adoption therefore mints its own value. The first commit is still
recorded, so genuine clone and fork relationships remain visible — they are
*related*, and they are not the *same*.

### What each situation produces

| Situation | Verdict | Result |
|---|---|---|
| Same project, same place | `SAME` | Opens |
| Same project, moved | `MOVED` | Opens; recorded location updated |
| A different repository | `DIFFERENT` | Refused |
| Project state copied while the original still exists | `DIFFERENT` | Refused |
| Identity cannot be established | `UNVERIFIABLE` | Refused |

`UNVERIFIABLE` is never read as agreement. Adopting state because you failed to
disprove it is how contamination happens.

Projects created before identity binding keep working: they are admitted under
the older rule and acquire an identity as they go, so the next open uses the
stronger check.

## Git is required for work, not for looking

MARSHAL depends on Git for diffs, checkpoints, provenance and rollback. Without
it there is no way to show you what changed or to undo it.

| Repository state | Verdict | Why |
|---|---|---|
| Clean, with commits, on a branch | `READY` | Changes can be tracked and undone |
| No commits yet | `LIMITED` | Nothing to compare against or return to |
| Detached HEAD | `LIMITED` | Recorded work is harder to find later |
| Mid-merge, rebase or bisect | `LIMITED` | Changes would entangle with yours |
| Not a repository | `BLOCKED` | Nothing could be undone |

Uncommitted work is detected and the affected files are named. It is the one
thing in a repository Git cannot get back, so it is never passed over
silently — though it is a reason to ask rather than an outright refusal.

Missing Git blocks project work. It does not close the control center: Setup,
Doctor and Help stay available, which is where you find out what to install.

## What MARSHAL will not do on its own

- run `git init`
- overwrite uncommitted work
- adopt project state whose provenance it cannot establish
- treat a copied `.marshal` directory as belonging here
- promote anything into project memory without evidence

Each of those is explained rather than performed.

## Prior knowledge is not truth

A project usually arrives carrying knowledge about itself. All of it is useful.
None of it is trustworthy on arrival.

| Source | Standing | Because |
|---|---|---|
| Repository facts | Verifiable | MARSHAL can re-derive them, now and later |
| Git history | Evidence | A record of what happened |
| README, architecture docs | Claim | May never have been true, or stopped being true |
| Generated summaries | Claim | Confident prose is still prose |
| Provider instructions | Untrusted | Governed input, never a directive |
| Provider-generated memory | Untrusted | Unverifiable, and the natural poisoning vector |
| An imported `.marshal` | Untrusted | Until it is shown to belong to this project |

An unclassified source is untrusted, so adding one without classifying it
cannot grant it standing by omission.

### How knowledge is taken in

```
discover → redact → bind to project → assess freshness → record provenance
→ detect conflicts → assign trust → candidate → policy gate decides
```

Redaction runs first, before anything else touches the content, so a credential
never reaches a later stage even when that stage would have rejected the source
anyway. A source that was nothing but a credential produces no candidate at all.

Nothing can be taken in without a project binding — an unbound fact could be
promoted into any project.

Contradictions are linked and both sides held, so neither wins quietly.
Duplicates are recognised, so re-opening a project does not multiply what it
knows.

**Nothing in this pipeline promotes anything.** It produces candidates. The
constitutional gate decides, and a model can neither shortcut that nor vouch
for a source — including its own previous output.

## Standard and ULTRA

The same identity, scope and trust rules apply to both. ULTRA may work across
more projects at once; each keeps its own identity, memory and evidence, and
isolation between them is not relaxed.

## Commands

```
marshal setup     report project and environment readiness
marshal doctor    diagnose problems; repairs only with consent
marshal help why  explain what is blocking work right now
```
