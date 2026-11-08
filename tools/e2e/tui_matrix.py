#!/usr/bin/env python3
"""Render tui_walk.py JSONL output as a Markdown matrix grouped by section.

Usage: tui_matrix.py tui-actions.jsonl > tui-actions.md
       tui_matrix.py --deep tui-actions.jsonl > deep.md   (a walk run with --deep)

--deep renders the per-action probe: empty submit, bad input, a well-formed
unknown value, Esc, and a PASS/FAIL/DEAD/HIDDEN verdict per action.
"""
import collections
import json
import sys


def deep_verdict(r):
    st = r["state"]
    d = r.get("deep", {})
    if st in ("ERROR", "UNREACHABLE", "CHANGED"):
        return "FAIL"
    if st == "DEAD":
        return "DEAD"
    if st == "HIDDEN":
        return "HIDDEN"
    if d:
        if d.get("esc") != "clean" or d.get("stray_search") or d.get("palette_leak") or d.get("panic"):
            return "FAIL"
        if st == "FORM":
            if d.get("required_fields") and not d.get("empty", "").startswith("REFUSED"):
                return "FAIL"
            for k in ("bad", "plausible"):
                if d.get(k, "").startswith(("STUCK", "LEFT", "could not")):
                    return "FAIL"
    return "PASS"


def cell(x):
    return (x or "").replace("|", "\\|").replace("\n", " ")[:160]


def deep_main(path):
    rows = [json.loads(l) for l in open(path) if l.startswith("{")]
    counts = collections.Counter(deep_verdict(r) for r in rows)
    print("Totals: " + ", ".join("%s %d" % kv for kv in sorted(counts.items())) + "\n")
    section = None
    for r in rows:
        sec = r.get("path", "?").split(" / ")[0]
        if sec != section:
            section = sec
            print("\n### %s\n" % sec)
            print("| ID | Path | Opens as | Detail / state shown | Empty submit | Bad input | Well-formed unknown | Esc | Verdict |")
            print("|---|---|---|---|---|---|---|---|---|")
        d = r.get("deep", {})
        esc = d.get("esc", "")
        if d.get("esc_status") and r["state"] in ("CONFIRM", "FORM"):
            esc += "; " + d["esc_status"]
        if r["state"] == "CONFIRM":
            detail = "Cancel focused" if d.get("default_cancel") else "Proceed focused"
            if d.get("destructive_ack"):
                detail += ", needs y"
        else:
            detail = r.get("detail", "")
        if d.get("not_run_yet"):
            detail = "[Not run yet] " + detail
        plaus = d.get("plausible", "")
        if d.get("plausible_cancel"):
            plaus += " -> Esc: " + d["plausible_cancel"]
        print("| %s | %s | %s | %s | %s | %s | %s | %s | %s |" % (r["id"], cell(r.get("path")), r["state"], cell(detail),
              cell(d.get("empty", "")), cell(d.get("bad", "")), cell(plaus), cell(esc), deep_verdict(r)))
    return 0


def main() -> int:
    if sys.argv[1:2] == ["--deep"]:
        return deep_main(sys.argv[2])
    rows = [json.loads(l) for l in open(sys.argv[1]) if l.startswith("{")]
    counts = collections.Counter(r.get("state") for r in rows)
    print("# Advanced menu (Ctrl+N) action matrix\n")
    print("States: FORM = typed form opened; CONFIRM = confirmation opened; RESULT = "
          "confirmation resolved immediately to a verdict (usually BLOCKED by state); "
          "DISABLED = refused with a reason; HIDDEN = intentionally not offered (this edition, or under construction: hidden until wired, cause kept); ERROR/DEAD/UNREACHABLE/CHANGED = broken.\n")
    print("Totals: " + ", ".join("%s %d" % kv for kv in sorted(counts.items())) + "\n")
    section = None
    for r in rows:
        sec = (r.get("path") or "?").split(" / ")[0]
        if sec != section:
            section = sec
            print("\n## %s\n" % sec)
            print("| ID | Type | Path | State | Detail |")
            print("|---|---|---|---|---|")
        detail = (r.get("detail") or r.get("status_line") or "").replace("|", "\\|")
        print("| %s | %s | %s | %s | %s |" % (r.get("id"), (r.get("type") or "").replace("_ACTION", ""),
                                            r.get("path", "").replace("|", "\\|"), r.get("state"), detail))
    return 0


if __name__ == "__main__":
    sys.exit(main())
