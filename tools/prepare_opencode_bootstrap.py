#!/usr/bin/env python3
"""Prepare exact official OpenCode libraries without npm/lifecycle scripts or auth."""
import argparse
import base64
import hashlib
import io
import json
import os
import re
from pathlib import Path, PurePosixPath
import shutil
import tarfile
import tempfile
import urllib.request

class NoRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, newurl):
        raise ValueError("package redirect refused")


DOWNLOAD = urllib.request.build_opener(NoRedirects())
LOCK = Path(__file__).with_name("opencode-bootstrap-lock.json")
MAX_FILE = 16 << 20
MAX_TOTAL = 128 << 20

def tree_digest(root):
    entries = []
    total = 0
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ValueError("bootstrap links refused")
        if path.is_dir():
            continue
        if not path.is_file():
            raise ValueError("bootstrap special files refused")
        rel = path.relative_to(root).as_posix()
        if rel not in ("opencode/package.json", "opencode/package-lock.json", "opencode/.gitignore") and not rel.startswith("opencode/node_modules/"):
            raise ValueError("foreign bootstrap configuration refused")
        raw = path.read_bytes()
        total += len(raw)
        if len(raw) > MAX_FILE or total > MAX_TOTAL:
            raise ValueError("bootstrap exceeds size bound")
        entries.append((rel, hashlib.sha256(raw).hexdigest()))
    h = hashlib.sha256()
    for rel, digest in sorted(entries):
        h.update(rel.encode() + b"\0" + digest.encode() + b"\n")
    return h.hexdigest()

def unpack(raw, destination):
    total = 0
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:gz") as archive:
        for member in archive:
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts or not path.parts or path.parts[0] != "package":
                raise ValueError("unsafe npm archive path")
            if member.isdir():
                continue
            if not member.isfile() or member.size > MAX_FILE:
                raise ValueError("npm links or special/oversize files refused")
            total += member.size
            if total > MAX_TOTAL:
                raise ValueError("npm archive exceeds bound")
            target = destination.joinpath(*path.parts[1:])
            if target.exists():
                raise ValueError("duplicate npm archive entry")
            target.parent.mkdir(parents=True, exist_ok=True)
            stream = archive.extractfile(member)
            with target.open("xb") as out:
                shutil.copyfileobj(stream, out)
            target.chmod(0o644)

def prepare(destination, lock):
    if destination.exists():
        if destination.is_symlink() or tree_digest(destination) != lock["tree_sha256"]:
            raise ValueError("existing bootstrap differs from pinned tree; choose a fresh destination")
        return
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".opencode-bootstrap-", dir=destination.parent) as temporary:
        root = Path(temporary)
        config = root / "opencode"
        config.mkdir()
        (config / ".gitignore").write_text("node_modules\npackage.json\npackage-lock.json\nbun.lock\n.gitignore")
        dependencies = {"@opencode-ai/plugin": lock["version"], "@opencode-ai/sdk": lock["version"]}
        package = {"name": "marshal-opencode-offline-bootstrap", "version": lock["version"], "private": True, "dependencies": dependencies}
        (config / "package.json").write_text(json.dumps(package, sort_keys=True, indent=2) + "\n")
        package_lock = {"name": package["name"], "version": package["version"], "lockfileVersion": 3, "requires": True, "packages": {"": package}}
        for entry in lock["packages"]:
            name, version = entry["name"], entry["version"]
            if not re.fullmatch(r"(?:@[a-z0-9_-]+/)?[a-z0-9_-]+", name) or not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.]+)?", version):
                raise ValueError("unsafe package identity refused")
            prefix = "https://registry.npmjs.org/" + name + "/-/"
            if not entry["url"].startswith(prefix) or not entry["integrity"].startswith("sha512-"):
                raise ValueError("nonofficial package endpoint or integrity refused")
            with DOWNLOAD.open(entry["url"], timeout=30) as response:
                if response.url != entry["url"]:
                    raise ValueError("package redirect refused")
                raw = response.read(MAX_TOTAL + 1)
            if len(raw) > MAX_TOTAL or hashlib.sha512(raw).digest() != base64.b64decode(entry["integrity"][7:], validate=True):
                raise ValueError("official npm integrity mismatch")
            target = config / "node_modules" / name
            unpack(raw, target)
            metadata = json.loads((target / "package.json").read_text())
            if metadata.get("name") != name or metadata.get("version") != version:
                raise ValueError("package identity mismatch")
            package_lock["packages"]["node_modules/" + name] = {"version": version, "resolved": entry["url"], "integrity": entry["integrity"]}
        (config / "package-lock.json").write_text(json.dumps(package_lock, sort_keys=True, indent=2) + "\n")
        digest = tree_digest(root)
        if digest != lock["tree_sha256"]:
            raise ValueError("prepared dependency tree differs from pin")
        print("bootstrap_tree_sha256=" + digest)
        os.rename(root, destination)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--destination", type=Path)
    args = parser.parse_args()
    lock = json.loads(LOCK.read_text())
    destination = args.destination or Path.home() / ".cache/marshal/opencode-bootstrap" / lock.get("bootstrap_revision", lock["version"])
    prepare(destination, lock)
    print("offline_bootstrap=" + str(destination))

if __name__ == "__main__":
    main()
