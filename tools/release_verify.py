
#!/usr/bin/env python3
from __future__ import annotations
import argparse, hashlib, json, shutil, subprocess
from pathlib import Path
from typing import Any

def verify_manifest(root: Path, manifest: dict[str, Any]) -> list[str]:
    errors=[]
    for item in manifest.get("files", []):
        rel=item.get("path")
        expected=item.get("sha256")
        if not isinstance(rel,str) or not isinstance(expected,str):
            errors.append("invalid manifest entry")
            continue
        p=root/rel
        if not p.is_file():
            errors.append(f"missing: {rel}")
            continue
        actual=hashlib.sha256(p.read_bytes()).hexdigest()
        if actual != expected:
            errors.append(f"hash mismatch: {rel}")
    return errors

def detect_verifiers() -> dict[str,bool]:
    return {name: shutil.which(name) is not None for name in ("cosign","minisign","gpg")}

def main() -> int:
    p=argparse.ArgumentParser()
    p.add_argument("root")
    p.add_argument("manifest")
    p.add_argument("--show-verifiers", action="store_true")
    args=p.parse_args()
    root=Path(args.root)
    manifest=json.loads(Path(args.manifest).read_text())
    errors=verify_manifest(root,manifest)
    payload={"status":"PASS" if not errors else "FAIL","errors":errors}
    if args.show_verifiers:
        payload["available_verifiers"]=detect_verifiers()
    print(json.dumps(payload,indent=2))
    return 0 if not errors else 1

if __name__ == "__main__":
    raise SystemExit(main())
