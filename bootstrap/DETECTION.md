# Bootstrap Detection Rules

Detect, do not guess.

## Stack Markers

Examples:

```text
pyproject.toml / requirements.txt → Python
package.json → Node/JS/TS
Cargo.toml → Rust
go.mod → Go
pom.xml / Gradle → JVM
Gemfile → Ruby
composer.json → PHP
```

Multiple stacks are allowed.

## Build/Test Commands

Derive from:
- repository docs,
- package scripts,
- Makefile/Taskfile/Justfile,
- CI workflows,
- existing AGENTS/CLAUDE/GEMINI context.

Do not invent conventional commands when the repository states different ones.

## Agent Files

Detect:
- AGENTS.md
- CLAUDE.md
- GEMINI.md
- CONTEXT.md
- CONVENTIONS.md
- tool-specific configuration.

## Governance

Detect:
- CODEOWNERS,
- CONTRIBUTING,
- SECURITY,
- release policy,
- protected workflows.

## Output

Machine-readable detection is provided by:

```bash
python tools/marshal.py detect-project .
```
