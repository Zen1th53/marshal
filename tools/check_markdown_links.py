#!/usr/bin/env python3
"""Validate local file and heading links in repository Markdown."""

from __future__ import annotations

import re
import sys
from pathlib import Path
from urllib.parse import unquote


LINK = re.compile(r"(?<!!)\[[^\]]*\]\(([^)]+)\)")
HEADING = re.compile(r"^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$")
SKIP_SCHEMES = ("http://", "https://", "mailto:", "tel:", "data:")


def slug(text: str) -> str:
    text = re.sub(r"<[^>]+>", "", text).strip().lower()
    text = re.sub(r"[^\w\- ]", "", text, flags=re.UNICODE)
    return re.sub(r"[\s-]+", "-", text).strip("-")


def anchors(path: Path) -> set[str]:
    found: set[str] = set()
    counts: dict[str, int] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        match = HEADING.match(line)
        if not match:
            continue
        base = slug(match.group(2))
        count = counts.get(base, 0)
        counts[base] = count + 1
        found.add(base if count == 0 else f"{base}-{count}")
    return found


def check_tree(root: Path) -> list[str]:
    failures: list[str] = []
    markdown = sorted(
        path for path in root.rglob("*.md") if ".git" not in path.parts and "node_modules" not in path.parts and ".marshal" not in path.parts
    )
    for source in markdown:
        text = source.read_text(encoding="utf-8")
        for line_number, line in enumerate(text.splitlines(), 1):
            for match in LINK.finditer(line):
                target = match.group(1).strip().split(maxsplit=1)[0].strip("<>")
                if not target or target.startswith(SKIP_SCHEMES):
                    continue
                file_part, _, fragment = target.partition("#")
                destination = source if not file_part else source.parent / unquote(file_part)
                destination = destination.resolve()
                try:
                    destination.relative_to(root.resolve())
                except ValueError:
                    failures.append(f"{source.relative_to(root)}:{line_number}: link escapes repository: {target}")
                    continue
                if not destination.exists():
                    failures.append(f"{source.relative_to(root)}:{line_number}: missing target: {target}")
                    continue
                if fragment and destination.suffix.lower() == ".md":
                    if unquote(fragment).lower() not in anchors(destination):
                        failures.append(f"{source.relative_to(root)}:{line_number}: missing anchor: {target}")
    return failures


def main() -> int:
    root = Path(sys.argv[1] if len(sys.argv) > 1 else ".").resolve()
    failures = check_tree(root)
    if failures:
        print("\n".join(failures))
        return 1
    print("Markdown links: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
