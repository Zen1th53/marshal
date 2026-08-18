# MARSHAL Release Productization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish an evidence-backed `v1.0.0-rc.1` MARSHAL release with professional documentation, Linux artifacts, checksums, SBOM, provenance, and verified installation.

**Architecture:** Preserve the completed runtime and add a thin release layer: build metadata in `internal/version`, native shell/Python release tooling under `tools/`, tag-triggered GitHub Actions, and a documentation portal that keeps existing links stable. Public claims are derived from source, tests, Git history, and published assets.

**Tech Stack:** Go 1.25, Python 3.11, GitHub Actions, GitHub CLI, CycloneDX JSON, GNU tar, SHA-256.

**Spec:** `docs/superpowers/specs/2026-08-18-release-productization-design.md`

## Global Constraints

- Public release version is `v1.0.0-rc.1`; pack version remains `6.0.0`.
- Linux amd64 and arm64 are the only binary release targets.
- Existing historical tags and license grants are immutable.
- No capability, provider maturity, test, security, or reproducibility claim without fresh evidence.
- No workflow receives broad permissions or pull-request secrets.

---

### Task 1: Establish release truth and version output

**Files:**
- Create: `internal/version/version.go`
- Create: `internal/version/version_test.go`
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/cli_test.go`

**Interfaces:**
- Produces: `version.Info` and `version.Current()`; CLI commands `version` and `--version`.

- [ ] Write tests asserting local `dev` output and structured JSON fields.
- [ ] Run `go test ./internal/version ./internal/cli -count=1` and verify the new tests fail.
- [ ] Implement linker-injectable version, commit, and build-date fields.
- [ ] Wire `version` and `--version` through the CLI without changing other commands.
- [ ] Run focused tests and commit `feat: add build-aware version reporting`.

### Task 2: Build deterministic release packaging and SBOM tooling

**Files:**
- Create: `tools/build_release.sh`
- Create: `tools/generate_sbom.py`
- Create: `tools/tests/test_generate_sbom.py`
- Create: `release/INSTALL.md`

**Interfaces:**
- Produces: Linux tar archives, `checksums.txt`, `marshal_<version>_sbom.cdx.json`, and `build-metadata.json` under a caller-selected staging directory.

- [ ] Write Python tests for deterministic CycloneDX component ordering and required metadata.
- [ ] Run the test and verify it fails because the generator is absent.
- [ ] Implement the SBOM generator from `go list -m -json all` output.
- [ ] Implement the release script with explicit version, commit, build time, target directory, and two Linux architectures.
- [ ] Build the release twice with identical inputs and compare hashes.
- [ ] Verify both archive binaries report the injected version.
- [ ] Commit `build: add deterministic Linux release packaging`.

### Task 3: Make documentation the product interface

**Files:**
- Replace: `README.md`
- Create: `docs/README.md`
- Create: `docs/installation.md`
- Create: `docs/operations/upgrades.md`
- Create: `docs/development/release-process.md`
- Create: `docs/security/supply-chain.md`
- Create: `examples/README.md`
- Create: `examples/policy-test-suite.yaml`
- Modify: `CHANGELOG.md`
- Modify: `SECURITY.md`
- Modify: `CONTRIBUTING.md`
- Modify: `docs/getting-started.md`
- Modify: `docs/a2a.md`

**Interfaces:**
- Produces: one landing page, one documentation index, verified source and binary installation paths, and release-candidate notes.

- [ ] Reconcile product, pack, protocol, and schema version language.
- [ ] Replace stale provider and runtime maturity claims with evidence classes.
- [ ] Add Mermaid architecture and trust-boundary diagrams.
- [ ] Document verified quick start, policy-test example, MCP/A2A status checks, upgrade safety, and artifact verification.
- [ ] Normalize the changelog around `v1.0.0-rc.1` while preserving actual historical tags.
- [ ] Commit `docs: establish release-ready MARSHAL documentation`.

### Task 4: Add community health and security automation

**Files:**
- Create: `CODE_OF_CONDUCT.md`
- Create: `SUPPORT.md`
- Create: `GOVERNANCE.md`
- Create: `.github/ISSUE_TEMPLATE/documentation.yml`
- Create: `.github/ISSUE_TEMPLATE/config.yml`
- Modify: `.github/ISSUE_TEMPLATE/bug_report.yml`
- Modify: `.github/ISSUE_TEMPLATE/feature_request.yml`
- Modify: `.github/pull_request_template.md`
- Create: `.github/dependabot.yml`
- Modify: `.github/workflows/ci.yml`
- Create: `.github/workflows/codeql.yml`
- Create: `.github/workflows/dependency-review.yml`

**Interfaces:**
- Produces: actionable issue/PR intake and least-privilege CI/security gates.

- [ ] Add required bug environment and redacted-log fields plus private security-report routing.
- [ ] Add concise support, conduct, and maintainer governance documents.
- [ ] Add formatting/build/govulncheck CI stages without weakening existing gates.
- [ ] Add pinned CodeQL and dependency-review workflows with read-only pull-request permissions.
- [ ] Add monthly Go module and GitHub Actions Dependabot updates.
- [ ] Validate workflows with `actionlint` and commit `ci: harden project and supply-chain gates`.

### Task 5: Add tag-driven release publication

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `release/RELEASE_NOTES_1.0.0-rc.1.md`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: `tools/build_release.sh` and the tracked release notes.
- Produces: GitHub Release assets and build-provenance attestations for `v*` tags.

- [ ] Add release staging and binary hygiene ignores without excluding fixtures.
- [ ] Write complete RC release notes with installation, verification, limitations, links, and license.
- [ ] Add a tag workflow with explicit contents, id-token, and attestations permissions.
- [ ] Build artifacts, attest them, and publish through authenticated `gh` without third-party release actions.
- [ ] Validate the workflow and commit `build: add secure GitHub release workflow`.

### Task 6: Verify, integrate, publish, and re-verify

**Files:**
- Create: `docs/releases/v1.0.0-rc.1-readiness.md`
- Modify: `distribution/PACK-MANIFEST.json`

**Interfaces:**
- Produces: auditable release-gate evidence and the public tag/release.

- [ ] Run gofmt verification, vet, unit tests, race tests, build, govulncheck, Python/conformance suites, release verifier, link checker, secret scan, workflow lint, and artifact checksum verification.
- [ ] Regenerate and deterministically verify the pack manifest.
- [ ] Commit the readiness evidence, push the branch, open a ready PR, and wait for required checks.
- [ ] Merge normally, fetch `origin/main`, and verify every release commit is reachable.
- [ ] Create and push annotated tag `v1.0.0-rc.1` under `Zen1th53` identity.
- [ ] Wait for the release workflow and verify the GitHub Release plus every asset.
- [ ] Clone the public tag into a temporary directory and run the documented install, doctor, daemon/status, policy-test, and task smoke flows.
- [ ] Download a published archive, verify `checksums.txt`, inspect its SBOM/build metadata, and run its binary.
- [ ] Update repository description/topics/settings and record any UI-only limitation.
