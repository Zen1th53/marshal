#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import subprocess
from pathlib import Path
from typing import Any, Iterable


def verify_manifest(
    root: Path,
    manifest: dict[str, Any],
    tracked_paths: Iterable[str] | None = None,
) -> list[str]:
    errors: list[str] = []
    listed: set[str] = set()
    for item in manifest.get("files", []):
        rel = item.get("path")
        expected = item.get("sha256")
        expected_bytes = item.get("bytes")
        if not isinstance(rel, str) or not isinstance(expected, str):
            errors.append("invalid manifest entry")
            continue
        listed.add(rel)
        path = root / rel
        if not path.is_file():
            errors.append(f"missing: {rel}")
            continue
        data = path.read_bytes()
        actual = hashlib.sha256(data).hexdigest()
        if actual != expected:
            errors.append(f"hash mismatch: {rel}")
        if isinstance(expected_bytes, int) and len(data) != expected_bytes:
            errors.append(f"size mismatch: {rel}")
    if tracked_paths is not None:
        excluded = set(manifest.get("excluded_paths", []))
        for rel in sorted(set(tracked_paths) - excluded - listed):
            errors.append(f"unlisted: {rel}")
    return errors


def generate_manifest(
    root: Path,
    tracked_paths: Iterable[str],
    *,
    pack_version: str,
    generated_date: str,
    excluded_paths: list[str],
) -> dict[str, Any]:
    excluded = set(excluded_paths)
    files: list[dict[str, Any]] = []
    for rel in sorted(set(tracked_paths) - excluded):
        path = root / rel
        if not path.is_file():
            raise ValueError(f"tracked path is not a file: {rel}")
        data = path.read_bytes()
        files.append({
            "path": rel,
            "sha256": hashlib.sha256(data).hexdigest(),
            "bytes": len(data),
        })
    return {
        "manifest_version": 1,
        "pack_version": pack_version,
        "generated_date": generated_date,
        "hash_algorithm": "sha256",
        "excluded_paths": excluded_paths,
        "files": files,
    }


def git_tracked_paths(root: Path) -> list[str]:
    process = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z"],
        capture_output=True,
        check=False,
    )
    if process.returncode != 0:
        raise RuntimeError("cannot enumerate tracked files")
    return [value.decode("utf-8") for value in process.stdout.split(b"\0") if value]


def detect_verifiers() -> dict[str, bool]:
    return {name: shutil.which(name) is not None for name in ("cosign", "minisign", "gpg")}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root")
    parser.add_argument("manifest")
    parser.add_argument("--show-verifiers", action="store_true")
    parser.add_argument("--generate", action="store_true")
    args = parser.parse_args()
    root = Path(args.root).resolve()
    manifest_path = Path(args.manifest).resolve()
    tracked = git_tracked_paths(root)
    if args.generate:
        previous = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest = generate_manifest(
            root,
            tracked,
            pack_version=previous["pack_version"],
            generated_date=previous["generated_date"],
            excluded_paths=previous["excluded_paths"],
        )
        temporary = manifest_path.with_suffix(manifest_path.suffix + ".tmp")
        temporary.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        temporary.replace(manifest_path)
    else:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    errors = verify_manifest(root, manifest, tracked)
    payload: dict[str, Any] = {"status": "PASS" if not errors else "FAIL", "errors": errors}
    if args.show_verifiers:
        payload["available_verifiers"] = detect_verifiers()
    print(json.dumps(payload, indent=2))
    return 0 if not errors else 1


if __name__ == "__main__":
    raise SystemExit(main())
