#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Prefer the pinned local environment; CI installs the same requirements.
if [[ -x "${HOME}/.cache/marshal-mkdocs-venv/bin/mkdocs" ]]; then
  docs_bin="${HOME}/.cache/marshal-mkdocs-venv/bin"
else
  docs_bin="$(dirname "$(command -v mkdocs)")"
fi
"${docs_bin}/mkdocs" build --strict --clean
"${docs_bin}/python" scripts/docs-audit.py
