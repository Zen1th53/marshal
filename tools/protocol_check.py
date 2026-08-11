
#!/usr/bin/env python3
from __future__ import annotations
import argparse, json, re

def a2a_compatible(client: str, server: str) -> bool:
    pat = re.compile(r"^(\d+)\.(\d+)$")
    cm = pat.match(client)
    sm = pat.match(server)
    if not cm or not sm:
        return False
    return cm.groups() == sm.groups()

def mcp_compatible(client: str, server: str) -> bool:
    # Agent OS pins the exact negotiated MCP specification date.
    return bool(re.match(r"^\d{4}-\d{2}-\d{2}$", client)) and client == server

def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("protocol", choices=["a2a","mcp"])
    p.add_argument("client")
    p.add_argument("server")
    args = p.parse_args()
    ok = a2a_compatible(args.client,args.server) if args.protocol=="a2a" else mcp_compatible(args.client,args.server)
    print(json.dumps({"protocol":args.protocol,"client":args.client,"server":args.server,"compatible":ok}, indent=2))
    return 0 if ok else 5

if __name__ == "__main__":
    raise SystemExit(main())
