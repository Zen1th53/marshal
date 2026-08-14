# Acquisition Due-Diligence Evidence Exporter

MARSHAL provides an automated, reproducible due-diligence auditing and evidence export subsystem via the `marshal legal` CLI suite.

This tool allows repository maintainers, prospective acquirers, and legal/IP counsel to inspect and export evidence regarding Git source history, committed licensing, chain-of-title documentation, contributor assignment records, third-party provenance, offline Go dependency licenses, and release integrity metadata.

---

## 1. CLI Usage

### Audit Repository Licensing & IP Evidence
Run an interactive terminal audit of current repository legal state:

```bash
marshal legal audit
```

Output structured machine-readable JSON audit report:

```bash
marshal legal audit --json
```

### Export Reproducible Evidence Pack
Export a deterministic, self-contained `.tar.gz` evidence pack:

```bash
marshal legal export --output /path/to/marshal-due-diligence.tar.gz
```

Output example:

```text
Evidence pack: /tmp/marshal-due-diligence.tar.gz
Source HEAD:   077ea70398de96d464c42d9ea9f3b7050f8cc153
SHA-256:       0824d38f2fc088d85e8b72ab5280fbedf9d2dc9cb7479d62d0bee6d6c52174b1
Status:        REVIEW_REQUIRED
```

---

## 2. Technical & Security Guarantees

1. **Committed HEAD Binding**: All repository evidence is read directly from committed Git blobs (`git show HEAD:<path>`). Uncommitted working-tree modifications are never packaged into evidence files.
2. **Dirty Tree Isolation**: If the working tree contains uncommitted changes, `working_tree_clean = false` is reported in `report.json` and `REPORT.md`, but the packaged evidence remains anchored to `HEAD`.
3. **Secret & Credential Safety**: The exporter never collects `.git/`, `.marshal/`, `.env`, API keys, tokens, runtime state, sockets, or user home directory files.
4. **Offline Dependency Collection**: Dependency license files are collected from local Go module cache without network calls (`GOPROXY=off`). Absolute host filesystem paths are sanitized.
5. **Path Traversal Safety**: Archive entry paths reject `..`, absolute paths, leading slashes, and NUL bytes.
6. **100% Deterministic Reproducibility**: Exporting the same commit SHA with identical dependency cache produces a byte-for-byte identical `.tar.gz` archive and matching archive SHA-256 hash.

---

## 3. Evidence Archive Structure

The exported `.tar.gz` contains the following layout:

```text
marshal-due-diligence/
├── REPORT.md
├── report.json
├── SHA256SUMS
│
├── source/
│   ├── commits.jsonl
│   ├── authors.json
│   └── source-state.json
│
├── licensing/
│   ├── LICENSE
│   ├── LICENSING.md
│   ├── COMMERCIAL-LICENSING.md
│   ├── THIRD_PARTY_NOTICES.md
│   ├── Apache-2.0.txt
│   └── LICENSE-HISTORY.md
│
├── ownership/
│   ├── CHAIN-OF-TITLE.md
│   ├── IP-PROVENANCE-AUDIT.md
│   ├── OWNER-AND-SUCCESSOR-MODEL.md
│   ├── CONTRIBUTOR-MODEL-DECISION.md
│   ├── INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md
│   ├── CORPORATE-CONTRIBUTOR-ASSIGNMENT.md
│   ├── assignment-registry.yml
│   ├── CONTRIBUTING-IP.md
│   └── contributor-rights-check.yml
│
├── third-party/
│   ├── CODE_OF_CONDUCT.md
│   ├── THIRD-PARTY-POLICY.md
│   ├── AI-CONTRIBUTION-POLICY.md
│   └── dependencies/
│       └── ...
│
└── integrity/
    ├── PACK-MANIFEST.json
    └── VERIFICATION.json
```

Internal archive integrity can be verified via:

```bash
cd marshal-due-diligence && sha256sum -c SHA256SUMS
```

---

## 4. Legal Status Disclaimer

> [!IMPORTANT]
> The generated evidence report and export pack represent mechanical evidence collection only. They do **not** constitute legal advice, a formal legal opinion, or a guarantee of legal perfection. Items containing `DRAFT` markers or unresolved `[COPYRIGHT OWNER NAME / DESIGNATED ENTITY]` placeholders remain explicitly marked as `REVIEW_REQUIRED` for human legal counsel review.
