# MARSHAL — IP Provenance Audit

**Audit Date**: 2026-08-12
**Auditor**: Automated provenance scan + manual review
**Repository**: `https://github.com/Zen1th53/marshal`
**HEAD at audit**: `23719e0` (branch `main`)
**Scope**: All source, documentation, schemas, fixtures, and dependencies

---

## 1. Git Authorship Analysis

### Commit Authors (all branches)

| Author | Email | Commits |
|---|---|---|
| Zen1th53 | `extreme29@proton.me` | 70 |

**Total unique authors: 1**

### Methodology

```bash
git shortlog -sne --all
git log --format='%aN <%aE>' --all | sort -u
```

### Finding

**OWNER-CREATED** — All 70 commits across all branches and tags are authored
by a single identity (`Zen1th53 <extreme29@proton.me>`). No external
contributor commits exist in the repository history.

---

## 2. Source Code Classification

### Go Source (71 files)

| Category | Classification | Notes |
|---|---|---|
| `cmd/marshal/` | OWNER-CREATED | CLI entry point |
| `internal/` | OWNER-CREATED | All runtime packages (adapter, artifact, auth, daemon, doctor, sandbox, store, task, worktree) |
| Top-level Go files | OWNER-CREATED | No vendored or copied code detected |

**No SPDX headers or copyright notices** are present in Go source files.
No `// copied from`, `// adapted from`, or similar provenance markers were found.

### Python Source

| Category | Classification | Notes |
|---|---|---|
| `conformance/runner.py` | OWNER-CREATED | Conformance test runner |
| `conformance/tests/` | OWNER-CREATED | Unit tests for conformance runner |
| `tools/release_verify.py` | OWNER-CREATED | Release verification tooling |
| `tools/tests/`, `tools/tests_v6/` | OWNER-CREATED | Test suites for tooling |

---

## 3. Third-Party In-Tree Content

### CODE_OF_CONDUCT.md

| Field | Value |
|---|---|
| Source | Contributor Covenant v3.0 |
| License | CC BY-SA 4.0 |
| Classification | **THIRD-PARTY** |
| Attribution | Properly attributed with license and source URL |
| Impact on relicensing | None — CC BY-SA 4.0 applies independently to this file; it does not affect project source licensing |

### No Other Third-Party Source

- No `vendor/` directory
- No copied/ported source code detected
- No embedded third-party libraries
- No external patches

---

## 4. Dependencies (Go Modules)

All dependencies are fetched via Go modules (`go.mod` / `go.sum`) and are
**not vendored** into the repository. They are **DEPENDENCY-ONLY**.

| Module | License | Classification |
|---|---|---|
| `go.yaml.in/yaml/v3` | MIT/Apache-2.0 | DEPENDENCY-ONLY |
| `modernc.org/sqlite` | BSD-3-Clause | DEPENDENCY-ONLY |
| `github.com/dustin/go-humanize` | MIT | DEPENDENCY-ONLY (indirect) |
| `github.com/google/uuid` | BSD-3-Clause | DEPENDENCY-ONLY (indirect) |
| `github.com/mattn/go-isatty` | MIT | DEPENDENCY-ONLY (indirect) |
| `github.com/ncruces/go-strftime` | MIT | DEPENDENCY-ONLY (indirect) |
| `github.com/remyoudompheng/bigfft` | BSD-3-Clause | DEPENDENCY-ONLY (indirect) |
| `golang.org/x/sys` | BSD-3-Clause | DEPENDENCY-ONLY (indirect) |
| `modernc.org/libc` | BSD-3-Clause | DEPENDENCY-ONLY (indirect) |
| `modernc.org/mathutil` | BSD-3-Clause | DEPENDENCY-ONLY (indirect) |
| `modernc.org/memory` | BSD-3-Clause | DEPENDENCY-ONLY (indirect) |

All dependency licenses are permissive (MIT, BSD-3-Clause, Apache-2.0) and
are compatible with both AGPL-3.0-only distribution and commercial/proprietary
licensing of the project-owned code.

No copyleft dependencies exist that would restrict the owner's licensing
flexibility for project-owned code.

---

## 5. Schemas and Protocol Definitions

| Path | Classification | Notes |
|---|---|---|
| `schemas/*.schema.json` (10 files) | OWNER-CREATED | Project-defined JSON schemas |
| `plugins/REGISTRY.schema.json` | OWNER-CREATED | Plugin registry schema |
| `tenancy/NAMESPACE.schema.json` | OWNER-CREATED | Namespace schema |

No schemas reference or incorporate third-party schema definitions.

---

## 6. Test Fixtures

| Path | Classification | Notes |
|---|---|---|
| `conformance/fixtures/` | OWNER-CREATED | All fixture JSON and README files |

No test fixtures contain copied third-party code.

---

## 7. Documentation

| Path | Classification | Notes |
|---|---|---|
| `README.md` | OWNER-CREATED | |
| `docs/` | OWNER-CREATED | Architecture, getting started, CLI, providers, etc. |
| `CONTRIBUTING.md` | OWNER-CREATED | Will be updated for new contribution model |
| `CODE_OF_CONDUCT.md` | THIRD-PARTY | Contributor Covenant 3.0 (CC BY-SA 4.0) |
| Agent role docs (`ARCHITECT.md`, etc.) | OWNER-CREATED | |
| Templates (`templates/`) | OWNER-CREATED | |

---

## 8. Generated Code

No auto-generated source code was detected in the repository. No `DO NOT EDIT`
markers or code generation tool outputs were found.

---

## 9. AI-Generated Code Provenance

The repository is an AI-agent engineering platform. The commit history shows
AI-assisted development tooling was used in the development process. However:

- All commits are authored by the repository owner
- No provenance markers indicating specific AI-generated code segments exist
- The owner represents the codebase as 100% owner-owned

Classification: **OWNER-CREATED** (owner is responsible for all committed code
regardless of tooling used in development).

---

## 10. Historical License State

| Tag | License | Classification |
|---|---|---|
| `runtime-v0.2.1` | Apache-2.0 | HISTORICAL |
| `runtime-v0.3.0` | Apache-2.0 | HISTORICAL |
| `runtime-v0.3.1` | Apache-2.0 | HISTORICAL |
| `runtime-v0.4.0` | Apache-2.0 | HISTORICAL |

All prior releases were distributed under Apache-2.0. Historical grants
associated with those distributed versions remain in effect for recipients
of those versions.

---

## 11. Summary

| Category | Count | Classification |
|---|---|---|
| Owner-created source | 71 Go + ~10 Python files | OWNER-CREATED |
| Owner-created docs | ~40+ files | OWNER-CREATED |
| Owner-created schemas | 12 files | OWNER-CREATED |
| Owner-created fixtures | ~10 directories | OWNER-CREATED |
| Third-party in-tree | 1 file (CODE_OF_CONDUCT.md) | THIRD-PARTY (CC BY-SA 4.0) |
| Dependencies | 11 Go modules | DEPENDENCY-ONLY (all permissive) |
| External contributors | 0 | N/A |
| Generated code | 0 | N/A |
| Unclear provenance | 0 | N/A |
| Legal review required | 0 | N/A |

---

## 12. Audit Conclusion

**The repository has clean IP provenance.**

- Single author, single copyright holder
- No external contributor code
- No vendored or copied third-party source
- All dependencies are permissive-licensed and fetched via package manager
- The sole in-tree third-party content (Contributor Covenant) is independently
  licensed under CC BY-SA 4.0 and does not affect project source licensing
- The owner's representation of 100% ownership is corroborated by the
  complete Git history

**No blocking issues identified for relicensing.**
