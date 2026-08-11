# Contributing to SLAVES

Contributions should preserve the project's evidence-first engineering model
and remain small enough to review and revert independently.

## Before starting

1. Read [TEAM.md](TEAM.md), [TORVALDS.md](TORVALDS.md), and the relevant role
   and protocol contracts.
2. Search existing issues and pull requests before duplicating work.
3. For non-trivial work, state the requirement, affected contracts, invariants,
   and verification plan before editing.
4. Report vulnerabilities privately according to [SECURITY.md](SECURITY.md).

## Workflow

Fork the repository or create a branch from the appropriate base. Preferred
branch names are:

```text
feat/<short-name>
fix/<short-name>
docs/<short-name>
```

Keep one active implementation task with one owner, branch, and worktree. Use
tests first for behavior changes. Do not mix unrelated cleanup, formatting,
dependency updates, or refactors into the patch.

Use Conventional Commit-style messages, for example:

```text
feat(runtime): add bounded worker shutdown
fix(policy): reject expired approval
docs: clarify adapter status
```

## Verification

Run the checks relevant to your change. The baseline repository suite is:

```bash
python conformance/runner.py validate-pack
python -m unittest discover -s conformance/tests -v
python -m unittest discover -s tools/tests -v
python -m unittest discover -s tools/tests_v6 -v
go test ./...
go vet ./...
```

Concurrency-sensitive runtime changes should also run:

```bash
go test -race ./...
```

Before committing, inspect `git diff --check`, the complete diff, and the exact
staged file list. Never claim a check passed unless you ran it on the reported
commit.

## Pull requests

A pull request should explain:

- the problem and minimum chosen solution;
- affected behavior and compatibility;
- tests and commands actually run;
- security, migration, or operational impact;
- verification not performed and known limitations.

Reviewers may require independent QA or AppSec evidence when role authority or
risk demands it. Developers cannot self-approve QA/AppSec findings.

## Security-sensitive changes

Changes involving authorization, secrets, process execution, filesystem or
network access, sandboxing, release trust, or external dependencies require
explicit threat-boundary analysis and negative tests. Do not weaken a control
silently to make a test pass.

## Licensing

SLAVES is licensed under Apache-2.0. Unless you explicitly state otherwise,
contributions intentionally submitted for inclusion are provided under the
repository license, consistent with Section 5 of the Apache License 2.0. This
project does not require a CLA or DCO at this time.

By participating, you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).
