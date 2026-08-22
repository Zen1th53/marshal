#!/usr/bin/env python3
"""Generate a deterministic CycloneDX module SBOM from `go list -m -json all`."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import pathlib
import urllib.parse


def parse_modules(payload: str) -> list[dict]:
    decoder = json.JSONDecoder()
    modules: list[dict] = []
    offset = 0
    while offset < len(payload):
        while offset < len(payload) and payload[offset].isspace():
            offset += 1
        if offset == len(payload):
            break
        value, offset = decoder.raw_decode(payload, offset)
        if not isinstance(value, dict):
            raise ValueError("Go module stream contains a non-object value")
        modules.append(value)
    return modules


def _version(value: str) -> str:
    return value[1:] if value.startswith("v") else value


def _purl(path: str, version: str) -> str:
    encoded_path = urllib.parse.quote(path, safe="/.-_~")
    encoded_version = urllib.parse.quote(version, safe=".-_~+")
    return f"pkg:golang/{encoded_path}@{encoded_version}"


def _go_sum_hash(value: str) -> list[dict[str, str]]:
    if not value.startswith("h1:"):
        return []
    try:
        digest = base64.b64decode(value[3:], validate=True)
    except (ValueError, base64.binascii.Error):
        return []
    if len(digest) != hashlib.sha256().digest_size:
        return []
    return [{"alg": "SHA-256", "content": digest.hex()}]


def build_bom(
    modules: list[dict], *, product_version: str, commit: str, build_time: str
) -> dict:
    main = next((module for module in modules if module.get("Main")), {})
    release_version = _version(product_version)
    components = []
    for module in modules:
        if module.get("Main"):
            continue
        path = str(module.get("Path", "")).strip()
        version = str(module.get("Version", "")).strip()
        if not path or not version:
            continue
        purl = _purl(path, _version(version))
        component = {
            "type": "library",
            "bom-ref": purl,
            "name": path,
            "version": _version(version),
            "purl": purl,
        }
        hashes = _go_sum_hash(str(module.get("Sum", "")))
        if hashes:
            component["hashes"] = hashes
        components.append(component)
    components.sort(key=lambda component: component["purl"])

    return {
        "bomFormat": "CycloneDX",
        "specVersion": "1.5",
        "version": 1,
        "metadata": {
            "timestamp": build_time,
            "component": {
                "type": "application",
                "bom-ref": _purl("github.com/Zen1th53/marshal", release_version),
                "name": "marshal",
                "version": release_version,
                "purl": _purl("github.com/Zen1th53/marshal", release_version),
            },
            "properties": [
                {"name": "marshal:commit", "value": commit},
                {
                    "name": "marshal:go-version",
                    "value": str(main.get("GoVersion", "unknown")),
                },
            ],
        },
        "components": components,
    }


def write_bom(path: pathlib.Path, bom: dict) -> None:
    path.write_text(json.dumps(bom, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--modules-json", type=pathlib.Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--build-time", required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()

    modules = parse_modules(args.modules_json.read_text(encoding="utf-8"))
    bom = build_bom(
        modules,
        product_version=args.version,
        commit=args.commit,
        build_time=args.build_time,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    write_bom(args.output, bom)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
