
#!/usr/bin/env python3
from __future__ import annotations
import argparse, json, subprocess
from typing import Any

def run_adapter(command: list[str], expected: str, timeout: int = 120) -> dict[str, Any]:
    try:
        proc = subprocess.run(command, text=True, capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        return {"status":"FAIL","reason":"timeout","expected":expected}
    if proc.returncode != 0:
        return {
            "status":"FAIL","reason":"adapter_nonzero","exit_code":proc.returncode,
            "stdout":proc.stdout[-4000:],"stderr":proc.stderr[-4000:],"expected":expected,
        }
    try:
        payload = json.loads(proc.stdout.strip())
    except json.JSONDecodeError:
        return {"status":"FAIL","reason":"invalid_json","stdout":proc.stdout[-4000:],"expected":expected}
    observed = payload.get("verdict")
    return {
        "status":"PASS" if observed == expected else "FAIL",
        "expected":expected,
        "observed":observed,
        "adapter_payload":payload,
    }

def main() -> int:
    p = argparse.ArgumentParser(description="Run one normalized behavioral conformance adapter")
    p.add_argument("--expected", required=True)
    p.add_argument("--timeout", type=int, default=120)
    p.add_argument("command", nargs=argparse.REMAINDER)
    args = p.parse_args()
    if not args.command:
        p.error("adapter command required after --")
    command = args.command[1:] if args.command and args.command[0] == "--" else args.command
    result = run_adapter(command, args.expected, args.timeout)
    print(json.dumps(result, indent=2))
    return 0 if result["status"] == "PASS" else 1

if __name__ == "__main__":
    raise SystemExit(main())
