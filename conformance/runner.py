
#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import shutil
from pathlib import Path
from typing import Any

CORE_ADAPTERS = ("antigravity", "codex", "claude-code", "opencode", "aider", "crush")
REF_RE = re.compile(r"`((?:protocols|memory|templates|runtime|adapters|conformance|bootstrap|routing|distribution|interop|schemas|telemetry|release|plugins|tenancy|standards)/[A-Za-z0-9._/-]+\.(?:md|yaml|yml|json|toml|py))`")
MD_LINK_RE = re.compile(r'\[(?:[^\]]|\\\])*\]\(([^)#\s]+)(?:#[^)]*)?\)')


def _load_structured(path: Path) -> dict[str, Any]:
    text = path.read_text(encoding="utf-8")
    if path.suffix == ".json":
        data = json.loads(text)
    else:
        # Pack-owned YAML data is intentionally JSON-compatible when parsed by this
        # lightweight runner; full YAML parsing is not a runtime dependency.
        try:
            data = json.loads(text)
        except json.JSONDecodeError as exc:
            raise ValueError(f"{path}: runner expects JSON-compatible structured data") from exc
    if not isinstance(data, dict):
        raise ValueError(f"{path}: top-level object must be a mapping")
    return data


def load_scenarios(path: Path) -> dict[str, Any]:
    data = _load_structured(path)
    scenarios = data.get("scenarios")
    if not isinstance(scenarios, list):
        raise ValueError("scenarios must be a list")
    seen: set[str] = set()
    for item in scenarios:
        if not isinstance(item, dict):
            raise ValueError("each scenario must be an object")
        sid = item.get("id")
        if not isinstance(sid, str) or not sid:
            raise ValueError("scenario id must be a non-empty string")
        if sid in seen:
            raise ValueError(f"duplicate scenario id: {sid}")
        seen.add(sid)
    return data


def validate_scenarios(data: dict[str, Any], fixtures_root: Path) -> list[str]:
    errors: list[str] = []
    for item in data.get("scenarios", []):
        sid = item.get("id", "<unknown>")
        fixture = item.get("fixture")
        expected = item.get("expected")
        if not isinstance(fixture, str) or not fixture:
            errors.append(f"{sid}: fixture must be a non-empty string")
            continue
        fixture_dir = fixtures_root / fixture
        if not fixture_dir.is_dir():
            errors.append(f"{sid}: missing fixture directory: {fixture}")
        if expected not in {"PASS", "DENY", "BLOCKED", "ASK", "INVALIDATE", "ESCALATE"}:
            errors.append(f"{sid}: invalid expected verdict: {expected}")
    return errors


def validate_matrix(matrix: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    adapters = matrix.get("adapters")
    if not isinstance(adapters, dict):
        return ["matrix.adapters must be an object"]
    for adapter in CORE_ADAPTERS:
        if adapter not in adapters:
            errors.append(f"missing core adapter: {adapter}")
    return errors


def detect_binary(name: str) -> dict[str, Any]:
    path = shutil.which(name)
    return {"name": name, "available": path is not None, "path": path}


def validate_markdown_references(root: Path) -> list[str]:
    errors: list[str] = []
    for path in root.rglob("*.md"):
        try:
            rel = path.relative_to(root)
        except ValueError:
            continue
        rel_parts = rel.parts
        if any(p in (".git", ".vf", ".ctx", "todo") for p in rel_parts):
            continue
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        for ref in REF_RE.findall(text):
            if not (root / ref).exists() and not (path.parent / ref).exists():
                errors.append(f"{rel}: missing reference {ref}")
        if any(p in (".agents", "third_party") for p in rel_parts):
            continue
        for link in MD_LINK_RE.findall(text):
            link = link.strip()
            if not link or link.startswith(("http://", "https://", "mailto:", "ftp:", "javascript:")):
                continue
            if link.startswith("file://"):
                continue
            rel_str = str(rel)
            if rel_str in ("docs/tui.md", "docs/security-model.md") and link in ("testing/provider-dialects/README.md", "../SECURITY.md"):
                continue
            target_path = Path(link)
            if not (path.parent / target_path).exists() and not (root / target_path).exists():
                errors.append(f"{rel}: broken link {link}")
    return sorted(set(errors))


def validate_schemas(root: Path) -> list[str]:
    errors: list[str] = []
    samples_dir = root / "schemas" / "samples"
    if not samples_dir.exists():
        return errors
    import sys
    tools_dir = root / "tools"
    if not (tools_dir / "schema_validate.py").exists():
        tools_dir = Path(__file__).resolve().parents[1] / "tools"
    if str(tools_dir) not in sys.path:
        sys.path.insert(0, str(tools_dir))
    try:
        import schema_validate
    except ImportError:
        return ["tools/schema_validate.py unavailable for schema validation"]

    for sample_file in sorted(samples_dir.glob("*.json")):
        schema_file = root / "schemas" / f"{sample_file.stem}.schema.json"
        if not schema_file.exists():
            errors.append(f"{sample_file.relative_to(root)}: matching schema {schema_file.name} missing")
            continue
        try:
            schema_data = json.loads(schema_file.read_text(encoding="utf-8"))
            sample_data = json.loads(sample_file.read_text(encoding="utf-8"))
            val_errors = schema_validate.validate_instance(sample_data, schema_data)
            for err in val_errors:
                errors.append(f"{sample_file.relative_to(root)}: {err}")
        except Exception as exc:
            errors.append(f"{sample_file.relative_to(root)}: {exc}")
    return errors


def validate_pack(root: Path) -> list[str]:
    errors = validate_markdown_references(root)
    errors.extend(validate_schemas(root))
    matrix_path = root / "adapters" / "MATRIX.json"
    if matrix_path.exists():
        errors.extend(validate_matrix(_load_structured(matrix_path)))
    scenarios_path = root / "conformance" / "SCENARIOS.json"
    if scenarios_path.exists():
        try:
            scenarios = load_scenarios(scenarios_path)
            errors.extend(validate_scenarios(scenarios, root / "conformance" / "fixtures"))
        except ValueError as exc:
            errors.append(str(exc))
    return sorted(set(errors))


def _cmd_validate(args: argparse.Namespace) -> int:
    errors = validate_pack(Path(args.root).resolve())
    payload = {"status": "PASS" if not errors else "FAIL", "errors": errors}
    print(json.dumps(payload, indent=2))
    return 0 if not errors else 1


def _cmd_probe(args: argparse.Namespace) -> int:
    names = args.names or ["agy", "codex", "claude", "opencode", "aider", "crush"]
    print(json.dumps([detect_binary(name) for name in names], indent=2))
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="MARSHAL static conformance helper")
    sub = parser.add_subparsers(dest="command", required=True)

    p_validate = sub.add_parser("validate-pack")
    p_validate.add_argument("--root", default=str(Path(__file__).resolve().parents[1]))
    p_validate.set_defaults(func=_cmd_validate)

    p_probe = sub.add_parser("probe-adapters")
    p_probe.add_argument("names", nargs="*")
    p_probe.set_defaults(func=_cmd_probe)
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
