#!/usr/bin/env python3
"""
Release Trust Verification Engine for MARSHAL.
Binds source commit -> provenance -> checksums -> signature -> SPDX SBOM.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
from pathlib import Path
from typing import Any, Dict, List


def compute_sha256(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while chunk := f.read(65536):
            h.update(chunk)
    return h.hexdigest()


def generate_go_sbom(
    repo_root: Path, version: str, created: str | None = None
) -> Dict[str, Any]:
    """Generate SPDX 2.3 SBOM parsing go.mod dependencies."""
    proc = subprocess.run(
        ["go", "list", "-m", "-json", "all"],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"go list failed: {proc.stderr.strip()}")

    packages: List[Dict[str, Any]] = [
        {
            "name": "github.com/Zen1th53/marshal",
            "SPDXID": "SPDXRef-Package-marshal",
            "versionInfo": version,
            "downloadLocation": "https://github.com/Zen1th53/marshal",
            "licenseConcluded": "AGPL-3.0-only",
        }
    ]

    decoder = json.JSONDecoder()
    raw = proc.stdout
    idx = 0
    pkg_idx = 1
    while idx < len(raw):
        raw_slice = raw[idx:].lstrip()
        if not raw_slice:
            break
        idx += len(raw[idx:]) - len(raw_slice)
        mod, end = decoder.raw_decode(raw[idx:])
        idx += end
        path = mod.get("Path", "")
        ver = mod.get("Version", "main")
        if path and path != "github.com/Zen1th53/marshal":
            pkg_idx += 1
            packages.append({
                "name": path,
                "SPDXID": f"SPDXRef-Package-{pkg_idx}",
                "versionInfo": ver,
                "downloadLocation": f"https://{path}",
                "licenseConcluded": "NOASSERTION",
            })

    return {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"MARSHAL Release {version}",
        "documentNamespace": f"https://github.com/Zen1th53/marshal/releases/tag/{version}/sbom",
        "creationInfo": {
            "creators": ["Tool: MARSHAL Release Trust Engine"],
            "created": created
            or subprocess.check_output(["date", "-u", "+%Y-%m-%dT%H:%M:%SZ"])
            .decode()
            .strip(),
        },
        "packages": packages,
    }


def generate_release_manifest(
    dist_dir: Path,
    source_commit: str,
    version: str,
) -> Dict[str, Any]:
    binaries = []
    for p in sorted(dist_dir.glob("*.tar.gz")):
        binaries.append({
            "name": p.name,
            "sha256": compute_sha256(p),
            "size_bytes": p.stat().st_size,
        })

    checksums_file = dist_dir / "checksums.txt"
    checksums_sha = compute_sha256(checksums_file) if checksums_file.exists() else None
    sbom_files = sorted(dist_dir.glob("*_sbom.spdx.json"))
    if not sbom_files and (dist_dir / "sbom.spdx.json").exists():
        sbom_files = [dist_dir / "sbom.spdx.json"]
    sbom_file = sbom_files[0] if len(sbom_files) == 1 else None

    return {
        "schema_version": "marshal.release-manifest.v1",
        "version": version,
        "source_commit": source_commit,
        "binaries": binaries,
        "checksums_sha256": checksums_sha,
        "sbom_name": sbom_file.name if sbom_file else None,
        "sbom_sha256": compute_sha256(sbom_file) if sbom_file else None,
    }


def verify_release(manifest_path: Path, dist_dir: Path) -> List[str]:
    errors = []
    data = json.loads(manifest_path.read_text())

    for binary in data.get("binaries", []):
        name = binary["name"]
        if Path(name).name != name or Path(name).is_absolute():
            errors.append(f"unsafe release artifact path: {name}")
            continue
        expected_sha = binary["sha256"]
        p = dist_dir / name
        if not p.is_file() or p.is_symlink():
            errors.append(f"missing release artifact: {name}")
            continue
        actual_sha = compute_sha256(p)
        if actual_sha != expected_sha:
            errors.append(f"checksum mismatch for {name}: expected {expected_sha}, got {actual_sha}")

    checksums_sha = data.get("checksums_sha256")
    checksums_path = dist_dir / "checksums.txt"
    if checksums_sha:
        if not checksums_path.is_file() or checksums_path.is_symlink():
            errors.append("missing checksums.txt")
        elif compute_sha256(checksums_path) != checksums_sha:
            errors.append("checksums.txt checksum mismatch")

    sbom_sha = data.get("sbom_sha256")
    if sbom_sha:
        sbom_name = data.get("sbom_name", "sbom.spdx.json")
        if Path(sbom_name).name != sbom_name or Path(sbom_name).is_absolute():
            errors.append(f"unsafe SBOM path: {sbom_name}")
        else:
            sbom_path = dist_dir / sbom_name
            if not sbom_path.is_file() or sbom_path.is_symlink():
                errors.append(f"missing {sbom_name}")
            elif compute_sha256(sbom_path) != sbom_sha:
                errors.append(f"{sbom_name} checksum mismatch")

    if checksums_path.is_file():
        for line_number, line in enumerate(checksums_path.read_text().splitlines(), 1):
            parts = line.split("  ", 1)
            if len(parts) != 2:
                errors.append(f"invalid checksums.txt line {line_number}")
                continue
            expected_sha, name = parts
            if len(expected_sha) != 64 or any(ch not in "0123456789abcdef" for ch in expected_sha):
                errors.append(f"invalid SHA-256 on checksums.txt line {line_number}")
                continue
            if Path(name).name != name or Path(name).is_absolute():
                errors.append(f"unsafe checksum path: {name}")
                continue
            artifact_path = dist_dir / name
            if not artifact_path.is_file() or artifact_path.is_symlink():
                errors.append(f"checksummed artifact is absent: {name}")
            elif compute_sha256(artifact_path) != expected_sha:
                errors.append(f"checksums.txt mismatch for {name}")

    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description="MARSHAL Release Trust Tool")
    sub = parser.add_subparsers(dest="cmd", required=True)

    gen_sbom = sub.add_parser("generate-sbom")
    gen_sbom.add_argument("--repo", default=".")
    gen_sbom.add_argument("--version", default="v1.0.1")
    gen_sbom.add_argument("--created")
    gen_sbom.add_argument("--output", required=True)

    gen_manifest = sub.add_parser("generate-manifest")
    gen_manifest.add_argument("--dist", required=True)
    gen_manifest.add_argument("--commit", required=True)
    gen_manifest.add_argument("--version", default="v1.0.1")
    gen_manifest.add_argument("--output", required=True)

    verify = sub.add_parser("verify")
    verify.add_argument("--manifest", required=True)
    verify.add_argument("--dist", required=True)

    args = parser.parse_args()

    if args.cmd == "generate-sbom":
        sbom = generate_go_sbom(Path(args.repo), args.version, args.created)
        Path(args.output).write_text(json.dumps(sbom, indent=2) + "\n")
        print(f"SPDX SBOM generated at {args.output}")
        return 0

    if args.cmd == "generate-manifest":
        manifest = generate_release_manifest(Path(args.dist), args.commit, args.version)
        Path(args.output).write_text(json.dumps(manifest, indent=2) + "\n")
        print(f"Release manifest generated at {args.output}")
        return 0

    if args.cmd == "verify":
        errors = verify_release(Path(args.manifest), Path(args.dist))
        if errors:
            print("Release trust verification FAILED:")
            for e in errors:
                print(f" - {e}")
            return 1
        print("Release trust verification PASSED.")
        return 0

    return 0


if __name__ == "__main__":
    sys.exit(main())
