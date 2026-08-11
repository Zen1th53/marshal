
#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
from pathlib import Path
from typing import Any

STACK_MARKERS = {
    "python": ["pyproject.toml", "requirements.txt", "setup.py", "setup.cfg"],
    "node": ["package.json", "pnpm-lock.yaml", "yarn.lock", "package-lock.json", "bun.lockb", "bun.lock"],
    "rust": ["Cargo.toml"],
    "go": ["go.mod"],
    "java": ["pom.xml", "build.gradle", "build.gradle.kts"],
    "ruby": ["Gemfile"],
    "php": ["composer.json"],
    "dotnet": ["*.sln", "*.csproj"],
}

INSTRUCTION_FILES = [
    "AGENTS.md",
    "CLAUDE.md",
    "GEMINI.md",
    "CONTEXT.md",
    "CONVENTIONS.md",
]

AGENT_BINARIES = ["gemini", "codex", "claude", "opencode", "aider", "crush"]


def _matches(root: Path, pattern: str) -> bool:
    if "*" in pattern:
        return any(root.glob(pattern))
    return (root / pattern).exists()


def detect_project(root: Path) -> dict[str, Any]:
    root = root.resolve()
    stacks = [
        name
        for name, markers in STACK_MARKERS.items()
        if any(_matches(root, marker) for marker in markers)
    ]
    ci: list[str] = []
    if (root / ".github" / "workflows").is_dir():
        ci.append("github-actions")
    if (root / ".gitlab-ci.yml").exists():
        ci.append("gitlab-ci")
    if (root / "Jenkinsfile").exists():
        ci.append("jenkins")

    instruction_files = [name for name in INSTRUCTION_FILES if (root / name).exists()]
    agents = []
    for binary in AGENT_BINARIES:
        path = shutil.which(binary)
        agents.append({"name": binary, "available": bool(path), "path": path})

    git = {"is_repo": False, "root": None, "branch": None, "commit": None}
    try:
        repo_root = subprocess.check_output(
            ["git", "-C", str(root), "rev-parse", "--show-toplevel"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
        git["is_repo"] = True
        git["root"] = repo_root
        git["commit"] = subprocess.check_output(
            ["git", "-C", str(root), "rev-parse", "HEAD"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
        try:
            git["branch"] = subprocess.check_output(
                ["git", "-C", str(root), "branch", "--show-current"],
                text=True,
                stderr=subprocess.DEVNULL,
            ).strip() or None
        except subprocess.CalledProcessError:
            pass
    except (subprocess.CalledProcessError, FileNotFoundError):
        pass

    return {
        "root": str(root),
        "stacks": sorted(stacks),
        "ci": ci,
        "instruction_files": instruction_files,
        "agents": agents,
        "git": git,
    }


def _nested_get(obj: dict[str, Any], dotted: str) -> Any:
    cur: Any = obj
    for part in dotted.split("."):
        if not isinstance(cur, dict) or part not in cur:
            return None
        cur = cur[part]
    return cur


def reconcile_state(file_state: dict[str, Any], runtime_state: dict[str, Any]) -> list[dict[str, Any]]:
    fields = (
        "task.id",
        "task.phase",
        "task.status",
        "project.branch",
        "project.commit",
    )
    conflicts: list[dict[str, Any]] = []
    for field in fields:
        left = _nested_get(file_state, field)
        right = _nested_get(runtime_state, field)
        if left is not None and right is not None and left != right:
            conflicts.append({
                "field": field,
                "file_state": left,
                "runtime_state": right,
            })
    return conflicts


def read_pack_version(root: Path) -> dict[str, Any]:
    path = root / "PACK-VERSION.yaml"
    text = path.read_text(encoding="utf-8")
    version_match = re.search(r'(?m)^pack_version:\s*["\']?([^"\'\n]+)', text)
    schema_match = re.search(r'(?m)^schema_version:\s*(\d+)', text)
    if not version_match or not schema_match:
        raise ValueError("invalid PACK-VERSION.yaml")
    return {
        "pack_version": version_match.group(1).strip(),
        "schema_version": int(schema_match.group(1)),
    }


def cmd_detect(args: argparse.Namespace) -> int:
    print(json.dumps(detect_project(Path(args.path)), indent=2))
    return 0


def cmd_pack_status(args: argparse.Namespace) -> int:
    print(json.dumps(read_pack_version(Path(args.root)), indent=2))
    return 0


def cmd_reconcile(args: argparse.Namespace) -> int:
    file_state = json.loads(Path(args.file_state).read_text(encoding="utf-8"))
    runtime_state = json.loads(Path(args.runtime_state).read_text(encoding="utf-8"))
    conflicts = reconcile_state(file_state, runtime_state)
    print(json.dumps({"status": "CLEAN" if not conflicts else "CONFLICT", "conflicts": conflicts}, indent=2))
    return 0 if not conflicts else 5


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Agent OS local helper")
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("detect-project")
    p.add_argument("path", nargs="?", default=".")
    p.set_defaults(func=cmd_detect)

    p = sub.add_parser("pack-status")
    p.add_argument("--root", default=str(Path(__file__).resolve().parents[1]))
    p.set_defaults(func=cmd_pack_status)

    p = sub.add_parser("reconcile-state")
    p.add_argument("--file-state", required=True)
    p.add_argument("--runtime-state", required=True)
    p.set_defaults(func=cmd_reconcile)

    return parser


def main() -> int:
    args = build_parser().parse_args()
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
