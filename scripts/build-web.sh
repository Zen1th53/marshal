#!/usr/bin/env bash
set -euo pipefail

# MARSHAL Web Control Plane — Production Build Script (T218)
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_DIR="${REPO_ROOT}/web"
DIST_TARGET="${REPO_ROOT}/internal/webcontrol/dist"

echo "[MARSHAL] Building production frontend assets in ${WEB_DIR}..."
cd "${WEB_DIR}"
npm run typecheck
npm run test:run
npm run build

echo "[MARSHAL] Syncing production distribution to ${DIST_TARGET}..."
mkdir -p "${DIST_TARGET}"
rm -rf "${DIST_TARGET:?}"/*
cp -r "${WEB_DIR}/dist"/* "${DIST_TARGET}/"

echo "[MARSHAL] Production frontend build verified & embedded successfully."
