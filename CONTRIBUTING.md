# Contributing to MARSHAL

Contributions should preserve the project's evidence-first engineering model
and remain small enough to review and revert independently.

## Before starting

1. Read [TEAM.md](TEAM.md), [TORVALDS.md](TORVALDS.md), and the relevant role
   and protocol contracts.
2. Search existing issues and pull requests before duplicating work.
3. For non-trivial work, state the requirement, affected contracts, invariants,
   and verification plan before editing.
4. Report vulnerabilities privately according to [SECURITY.md](SECURITY.md).
5. **Review IP Contributor Requirements**: Material external contributions require an executed Contributor Assignment Agreement before merge (see [Contributor Assignment Workflow](#contributor-assignment-workflow) below).

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

## Contributor Assignment Workflow

> [!IMPORTANT]
> **Material external contributions are not accepted for merge until the required contributor assignment process is completed.**

To preserve a clean chain-of-title and support the project's dual-licensing architecture (`AGPL-3.0-only` community + commercial), MARSHAL requires external contributors to execute a Copyright Assignment Agreement prior to merging material code changes.

```text
                     Pull Request Opened
                             │
            Contributor Identity Resolution
                             │
     ┌───────────────────────┴───────────────────────┐
     │                                               │
Individual Contributor                    Corporate Contributor
     │                                               │
Individual Agreement (ICAA)             Corporate Agreement (CCAA)
     │                                               │
     └───────────────────────┬───────────────────────┘
                             │
               Assignment Registry Check (CI Gate)
                             │
                  IP Provenance & Technical Review
                             │
                           Merge
```

### Contributor Agreement Types

* **Individual Contributors**: Execute the [Individual Contributor Assignment Agreement (ICAA)](legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md).
* **Corporate / Employer-Owned Contributions**: Execute the [Corporate Contributor Assignment Agreement (CCAA)](legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md).

For step-by-step onboarding, see [docs/legal/CONTRIBUTOR-ONBOARDING.md](docs/legal/CONTRIBUTOR-ONBOARDING.md).

> [!WARNING]
> Simply opening a Pull Request, checking a PR template checkbox, including a `Signed-off-by` line, or declaring DCO compliance does **NOT** automatically transfer copyright or replace an executed assignment agreement.

## Third-Party & AI-Generated Material

* **Third-Party Code**: Any third-party dependencies, external libraries, or copied code must be explicitly disclosed in the PR description. See [docs/legal/THIRD-PARTY-POLICY.md](docs/legal/THIRD-PARTY-POLICY.md).
* **AI-Generated Code**: Any material code produced using external AI systems must comply with [docs/legal/AI-CONTRIBUTION-POLICY.md](docs/legal/AI-CONTRIBUTION-POLICY.md) and be disclosed.

## Licensing

MARSHAL Community Edition is licensed under `AGPL-3.0-only`. Alternative Commercial Licensing is available for organizations requiring non-AGPL terms. See [LICENSING.md](LICENSING.md) and [COMMERCIAL-LICENSING.md](COMMERCIAL-LICENSING.md).

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
