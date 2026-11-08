#!/usr/bin/env python3
"""Minimal MCP stdio client used by extreme-e2e.sh.

Starts `marshal mcp serve --stdio` in PROJECT, performs initialize,
tools/list and a list of tools/call requests, and prints one JSON object
per line: {"step": ..., "ok": bool, "result"|"error": ...}.

Usage: mcp_probe.py MARSHAL_BIN PROJECT [TOOL JSON_ARGS]...
"""
import json
import subprocess
import sys
import threading


def main() -> int:
    if len(sys.argv) < 3 or (len(sys.argv) - 3) % 2:
        print(__doc__, file=sys.stderr)
        return 2
    binary, project = sys.argv[1], sys.argv[2]
    calls = [(sys.argv[i], json.loads(sys.argv[i + 1])) for i in range(3, len(sys.argv), 2)]
    proc = subprocess.Popen([binary, "mcp", "serve", "--stdio", "--project", project],
                            cwd=project, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True)
    timer = threading.Timer(60, proc.kill)
    timer.start()
    next_id = [0]

    def request(method, params, notify=False):
        msg = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notify:
            next_id[0] += 1
            msg["id"] = next_id[0]
        proc.stdin.write(json.dumps(msg) + "\n")
        proc.stdin.flush()
        if notify:
            return None
        while True:
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError("server closed stdout; stderr=" + proc.stderr.read()[-2000:])
            try:
                reply = json.loads(line)
            except ValueError:
                continue
            if reply.get("id") == msg["id"]:
                return reply

    status = 0
    try:
        init = request("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                      "clientInfo": {"name": "marshal-e2e", "version": "1"}})
        print(json.dumps({"step": "initialize", "ok": "result" in init, "result": init.get("result"), "error": init.get("error")}))
        request("notifications/initialized", {}, notify=True)
        tools = request("tools/list", {})
        names = [t.get("name") for t in (tools.get("result") or {}).get("tools", [])]
        print(json.dumps({"step": "tools/list", "ok": bool(names), "result": names, "error": tools.get("error")}))
        for name, args in calls:
            reply = request("tools/call", {"name": name, "arguments": args})
            result = reply.get("result") or {}
            ok = "result" in reply and not result.get("isError")
            if not ok:
                status = 1
            text = " ".join(c.get("text", "") for c in result.get("content", []) if isinstance(c, dict))
            print(json.dumps({"step": "call " + name, "ok": ok, "result": text[:1500], "error": reply.get("error")}))
    except Exception as exc:  # noqa: BLE001 - report every failure as a FAIL line
        print(json.dumps({"step": "exception", "ok": False, "error": str(exc)}))
        status = 1
    finally:
        timer.cancel()
        try:
            proc.stdin.close()
            proc.wait(timeout=10)
        except Exception:  # noqa: BLE001
            proc.kill()
    print(json.dumps({"step": "stop", "ok": True, "result": "exit=%s" % proc.returncode}))
    return status


if __name__ == "__main__":
    sys.exit(main())
