# MARSHAL Clean-Break Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make MARSHAL the sole identity across source, runtime behavior, documentation, generated artifacts, Git, and GitHub.

**Architecture:** Treat the rename as a breaking contract migration. Change observable identifiers test-first, mechanically migrate the Go namespace and reviewed text surfaces, move ignored local state without staging it, regenerate integrity metadata, then rename GitHub only after local verification succeeds.

**Tech Stack:** Go 1.25, Python 3 unittest tooling, SQLite runtime state, Git, GitHub Actions, GitHub CLI/API.

## Global Constraints

- Product display name is exactly `MARSHAL`.
- CLI, repository, lowercase identifiers, and internal naming use exactly `marshal`.
- Go module identity is exactly `github.com/Zen1th53/marshal`.
- Environment variables use only the `MARSHAL_` prefix.
- `.marshal/` is the only runtime and state directory.
- No compatibility alias or shim is retained.
- No dependency upgrades or unrelated cleanup are permitted.
- Local databases, tokens, credentials, keys, logs, artifacts, and machine state must not be committed.
- Historical Git objects and tags are not rewritten.

---

### Task 1: Lock the New Runtime Contracts in Tests

**Files:**
- Modify: `internal/project/layout_test.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `internal/auth/auth_test.go`
- Modify: `internal/mcp/mcp_test.go`
- Modify: `internal/sandbox/bwrap_test.go`
- Modify: `internal/worktree/manager_test.go`
- Modify: `internal/adapter/opencode/opencode_test.go`
- Modify: `internal/integration/*_test.go`
- Modify: `internal/legal/*_test.go`

**Interfaces:**
- Consumes: existing public and package-local runtime APIs.
- Produces: executable assertions for MARSHAL paths, output, environment variables, protocol identifiers, archive/schema identifiers, sandbox home, and branch naming.

- [ ] **Step 1: Change one layout assertion to the required runtime directory**

Add to `internal/project/layout_test.go` after repository discovery:

```go
if got, want := layout.RuntimeDir, filepath.Join(repo.Path(), ".marshal"); got != want {
	t.Fatalf("RuntimeDir = %q, want %q", got, want)
}
```

- [ ] **Step 2: Run the layout test and verify RED**

Run: `go test ./internal/project -run TestDiscover -count=1`

Expected: FAIL because discovery still returns the pre-migration runtime directory.

- [ ] **Step 3: Update existing behavioral expectations to MARSHAL**

Change expected CLI usage and display text to `marshal`/`MARSHAL`; token prefixes to `marshal_token_`; environment variables to `MARSHAL_*`; MCP server/tool names to `marshal-mcp-server` and `marshal_status`; schema/archive names to `marshal.*` and `marshal-*`; sandbox paths to `/home/marshal`; and task branches to `marshal/<task-id>`.

- [ ] **Step 4: Run affected tests and verify they fail for the intended identity mismatch**

Run:

```bash
go test ./internal/project ./internal/cli ./internal/auth ./internal/mcp \
  ./internal/sandbox ./internal/worktree ./internal/adapter/opencode \
  ./internal/integration ./internal/legal -count=1
```

Expected: FAIL only where production code still exposes the pre-migration identity.

### Task 2: Migrate Executable and Go Runtime Identity

**Files:**
- Rename: `cmd/<pre-migration-command>/` to `cmd/marshal/`
- Modify: `go.mod`
- Modify: all tracked `*.go` files containing the pre-migration module or product identifiers
- Modify: `.gitignore`

**Interfaces:**
- Consumes: MARSHAL expectations from Task 1.
- Produces: `github.com/Zen1th53/marshal/...` packages and a `marshal` executable that exclusively uses MARSHAL runtime contracts.

- [ ] **Step 1: Rename the command directory**

Use `git mv` to move the single current command directory to `cmd/marshal`.

- [ ] **Step 2: Update the module and imports**

Change the module declaration and every internal import to `github.com/Zen1th53/marshal`.

- [ ] **Step 3: Update production identity constants and strings**

Change runtime paths, sandbox home, CLI usage/output, token prefix, MCP server/tool identifiers, environment variables, task branch prefix, evidence schema/archive names, test Git identity, and all application-facing labels to MARSHAL.

- [ ] **Step 4: Update `.gitignore`**

Ensure `.marshal/` is ignored and no pre-migration runtime directory rule remains.

- [ ] **Step 5: Format and run the affected Go tests**

Run:

```bash
gofmt -w cmd internal
go test ./internal/project ./internal/cli ./internal/auth ./internal/mcp \
  ./internal/sandbox ./internal/worktree ./internal/adapter/opencode \
  ./internal/integration ./internal/legal -count=1
```

Expected: PASS.

### Task 3: Rebrand Every Tracked Project Surface

**Files:**
- Modify: `README.md`
- Modify: `.github/**`
- Modify: all tracked `*.md`, `*.json`, `*.yaml`, `*.yml`, `*.toml`, `*.py`, and shell sources returned by the preflight branding audit
- Modify: `distribution/PACK-MANIFEST.json` only through regeneration in Task 4

**Interfaces:**
- Consumes: canonical MARSHAL identity and security-first positioning.
- Produces: consistent documentation, examples, fixtures, legal text, GitHub templates, release instructions, badges, URLs, and helper behavior.

- [ ] **Step 1: Rewrite the README opening**

Use this exact hierarchy:

```markdown
# MARSHAL

**Vibe coding, without Vulnerability-as-a-Service.**
```

Follow it with security-first runtime/control-plane copy covering isolation, policy enforcement, least privilege, approvals, evidence, verification, auditability, reproducibility, vendor neutrality, and controlled autonomy. Include `Move fast. Grant less. Verify everything.`

- [ ] **Step 2: Migrate reviewed documentation and metadata occurrences**

For each preflight match, replace only project-identity uses with the appropriate `MARSHAL`, `marshal`, `MARSHAL_*`, `.marshal/`, or canonical GitHub URL. Preserve unrelated prose and third-party names.

- [ ] **Step 3: Update Python helpers and fixtures**

Change temporary directory prefixes, parser descriptions, expected executable path, module/package references, and fixture prose to MARSHAL.

- [ ] **Step 4: Run documentation/reference checks**

Run:

```bash
python3 conformance/runner.py validate-pack
python3 -m unittest discover -s conformance/tests -v
python3 -m unittest discover -s tools/tests -v
python3 -m unittest discover -s tools/tests_v6 -v
```

Expected: tests may fail only because the tracked pack manifest has not yet been regenerated.

### Task 4: Move Local State and Regenerate Integrity Metadata

**Files:**
- Move ignored local runtime directory to `.marshal/`
- Regenerate: `distribution/PACK-MANIFEST.json`

**Interfaces:**
- Consumes: renamed tracked tree and the ignored runtime state discovered during preflight.
- Produces: preserved local state under the sole supported path and a reproducible manifest matching tracked files.

- [ ] **Step 1: Confirm source and destination state before moving**

Inspect directory existence, permissions, file types, symlinks, and destination collisions. Abort on any collision rather than overwriting data.

- [ ] **Step 2: Move the ignored runtime directory atomically within the checkout**

Rename the existing ignored directory to `.marshal`. Confirm the database, artifacts, logs, and worktree entries remain present and the source directory is absent.

- [ ] **Step 3: Confirm runtime state remains ignored and unstaged**

Run: `git status --short --ignored`

Expected: `.marshal/` is ignored; no database, socket, PID, log, artifact, key, or worktree content is staged.

- [ ] **Step 4: Regenerate the pack manifest**

Run: `python3 tools/release_verify.py --generate . distribution/PACK-MANIFEST.json`

- [ ] **Step 5: Verify the regenerated manifest**

Run: `python3 tools/release_verify.py . distribution/PACK-MANIFEST.json`

Expected: PASS.

### Task 5: Exhaustive Local Verification

**Files:**
- Modify only files required to correct verified migration defects.

**Interfaces:**
- Consumes: the complete renamed tree.
- Produces: reproducible evidence that code, CLI, integrations, docs, and generated metadata are coherent.

- [ ] **Step 1: Run formatting and whitespace checks**

Run:

```bash
test -z "$(gofmt -l .)"
git diff --check origin/main...HEAD
```

- [ ] **Step 2: Build and smoke-test the renamed CLI**

Run:

```bash
go build -o /tmp/marshal ./cmd/marshal
/tmp/marshal --help
/tmp/marshal init --help
/tmp/marshal doctor
```

- [ ] **Step 3: Run Go verification separately**

Run:

```bash
go test ./...
go vet ./...
go test -race -count=1 ./...
```

- [ ] **Step 4: Run Python and pack verification separately**

Run:

```bash
python3 conformance/runner.py validate-pack
python3 -m unittest discover -s conformance/tests -v
python3 -m unittest discover -s tools/tests -v
python3 -m unittest discover -s tools/tests_v6 -v
python3 tools/release_verify.py . distribution/PACK-MANIFEST.json
```

- [ ] **Step 5: Run exhaustive zero-occurrence audits**

Search tracked content with `git grep`, all hidden worktree content excluding `.git` with `rg --hidden`, and filenames with `find`. Search every case variant and manually inspect any match. The required result is zero relevant occurrences and no pre-migration named path.

- [ ] **Step 6: Audit staged content for secrets and private state**

Inspect `git status`, `git diff`, `git diff --cached`, file types, suspicious credential/key patterns, database files, sockets, PID files, environment files, logs, artifacts, and machine-specific absolute paths. Required result: none staged or committed.

### Task 6: Commit and Migrate GitHub Identity

**Files:**
- No additional source changes unless remote verification exposes a genuine defect.

**Interfaces:**
- Consumes: verified local migration.
- Produces: canonical `Zen1th53/marshal` repository, remote, metadata, pushed commit, and observed CI state.

- [ ] **Step 1: Review final branch scope and commit identity**

Run:

```bash
git config user.name
git config user.email
git status
git diff --check
git log --oneline origin/main..HEAD
git diff --stat origin/main...HEAD
git diff origin/main...HEAD
```

- [ ] **Step 2: Commit the implementation**

Commit with subject `refactor!: rename project to MARSHAL` and a factual body covering product, namespace, CLI, docs, repository migration intent, and exact verification performed. Do not add tool or AI attribution.

- [ ] **Step 3: Verify GitHub identity and inspect supported rename syntax**

Run `gh auth status`, require `gh api user --jq .login` to equal `Zen1th53`, inspect `gh repo rename --help`, and inspect the repository API contract before writing.

- [ ] **Step 4: Rename and configure GitHub**

Rename the repository to `marshal`; set the exact required description; set only the approved focused topics; confirm default branch `main`; and verify Actions configuration remains enabled.

- [ ] **Step 5: Update and verify the canonical remote**

Set `origin` to `git@github.com:Zen1th53/marshal.git` or the equivalent authenticated HTTPS URL. Verify `git fetch origin` and a non-mutating push dry-run where supported.

- [ ] **Step 6: Push without bypassing protection**

Push `refactor/marshal`. If direct default-branch updates are protected, open a focused PR rather than bypassing protection. Merge only through the repository's permitted workflow.

- [ ] **Step 7: Verify GitHub result and CI**

Confirm repository URL/name, description, topics, remote, default branch, rendered README source, pushed commit SHA, and the resulting GitHub Actions run. Report pending or skipped external checks honestly.
