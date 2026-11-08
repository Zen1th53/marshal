#!/usr/bin/env python3
"""Walk every action of the Ctrl+N advanced menu through tmux.

Each action is reached through the menu search (Ctrl+K + its menu path), its
detail pane is captured, Enter is pressed once (which, by contract, opens a
form or a confirmation and never runs anything), the result is captured and
classified, and the walker backs out with Esc. It never selects Proceed and
never submits a form.

Usage:
  tui_walk.py --target SESSION:WINDOW --manifest internal/tui/manifest.json \
              --out DIR [--types ACTION,GOVERNED_ACTION,...] [--only CTUI-0077,...]
              [--wired internal/tui/wired.json]

Writes DIR/<spec>-pre.txt, DIR/<spec>-post.txt and prints one JSON line per
action: {"id","type","path","reached","state","detail","status_line","verdict"}.
States: FORM, CONFIRM, DISABLED, HIDDEN, ERROR, DEAD, UNREACHABLE.
Requires TMUX_TMPDIR to point at the isolated server that owns SESSION.

The menu hides every entry wired.json marks under construction (owner
decision 2026-11-05: no dead or WIP entry is visible). An entry the palette
does not list and --wired marks under construction is recorded HIDDEN with
that cause, so the matrix keeps it. To see whether a hidden entry has been
wired since, walk a TUI started with MARSHAL_TUI_SHOW_WIP=1, which lists those
entries marked [WIP], and regenerate wired.json from that walk first.

  tui_walk.py wired --matrix tools/e2e/results/<commit>-menu-actions.md \
              --manifest internal/tui/manifest.json --tasks tasks \
              --out internal/tui/wired.json [--check] [--base OLD.json]

Turns a recorded matrix into the wired status the Ctrl+N menu renders
(internal/tui/wired.json). --check regenerates from the matrix the file names
and fails when the committed file differs; --base fails when a spec that was
live is now under construction and no task file names it.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time

ACTION_TYPES = ("ACTION", "GOVERNED_ACTION", "DESTRUCTIVE_ACTION", "SENSITIVE_ACTION")
ROOT = "MARSHAL — COMMUNITY TUI / "
# Entries Extreme does not offer (ULTRA is local), so the palette never lists them.
EXTREME_HIDDEN = {"CTUI-0035", "CTUI-0157", "CTUI-0557", "CTUI-0562"}


def tmux(*args):
    return subprocess.run(["tmux", *args], capture_output=True, text=True, check=False)


# Status lines the background refresh writes over an action's own status.
STATUS_NOISE = {"refreshed", "refreshing…", "reading canonical state…"}


class Pane:
    def __init__(self, target):
        self.target = target

    def cap(self):
        return tmux("capture-pane", "-p", "-t", self.target).stdout

    def keys(self, *keys):
        tmux("send-keys", "-t", self.target, *keys)

    def lit(self, text):
        tmux("send-keys", "-t", self.target, "-l", text)


def is_landing(screen):
    first = screen.splitlines()[0] if screen else ""
    return first.lstrip().startswith("MARSHAL")


def is_nav(screen):
    first = screen.splitlines()[0] if screen else ""
    # The section bar abbreviates its titles on a narrow pane ("1 Hom",
    # "2 Con"), so match the numbered initials, not the whole words.
    # At the narrowest widths the selected title is bracketed and the
    # space goes: "[1Hom]  2Con".
    return re.search(r"1 ?H.*2 ?C.*9 ?S", first) is not None


def overlay(screen):
    if "Esc or Enter to dismiss" in screen:
        return "result"
    if "Search:" in screen and "Enter go" in screen:
        return "palette"
    if "typed input" in screen or "Type to edit" in screen:
        return "form"
    if re.search(r"Cancel\s+.?Proceed", screen):
        return "confirm"
    if "Keys" in screen and "help" in screen.lower() and "Esc close" in screen:
        return "help"
    return ""


def status_line(screen):
    for line in screen.splitlines()[:6]:
        if line.startswith("→"):
            return line[1:].strip()
    return ""


def reset(pane, deadline=12):
    """Bring the pane to the advanced menu with no overlay open."""
    for _ in range(deadline):
        screen = pane.cap()
        if is_landing(screen) and not is_nav(screen):
            pane.keys("C-n")
        elif is_nav(screen) and not overlay(screen):
            return True
        else:
            pane.keys("Escape")
        time.sleep(0.6)
    return False


def unavailable_reason(screen):
    """Return the refusal the detail pane shows for an unwired entry.

    The status line is shared with the background refresh, which can replace
    the refusal ("refreshed") before the capture, and it truncates long text
    with an ellipsis. The detail pane keeps the whole reason, wrapped, after
    "UNAVAILABLE:" and up to the first blank line.
    """
    right = [l.split("│", 1)[1].strip() if "│" in l else None for l in screen.splitlines()]
    for i, text in enumerate(right):
        if text and text.startswith("UNAVAILABLE:"):
            parts = [text[len("UNAVAILABLE:"):].strip()]
            for more in right[i + 1:]:
                if not more:
                    break
                parts.append(more)
            return " ".join(p for p in parts if p)
    return ""


def normalise(screen):
    screen = re.sub(r"read [^·]*ago", "read", screen)
    return "\n".join(line.rstrip() for line in screen.splitlines() if line.strip(" │"))


def walk(pane, node, outdir, wired=None):
    path = node["menu_path"].replace(ROOT, "")
    rec = {"id": node["spec_id"], "type": node["type"], "path": path}
    if not reset(pane):
        rec.update(reached=False, state="UNREACHABLE", detail="could not reset to advanced menu")
        return rec
    pane.keys("C-k")  # "/" in the menu now leaves it for the command line
    time.sleep(0.4)
    query = " ".join(w for w in re.split(r"[\s/]+", path.lower()) if w and w not in ("&", "→", "-"))
    pane.lit(query)
    time.sleep(0.8)
    screen = pane.cap()
    # Pick the result whose text equals the action's menu path. The palette
    # shows section titles without their " — Process NN" suffix.
    want = re.sub(r" — Process \d+", "", path)
    found = False
    lines = screen.splitlines()
    try:
        top = next(i for i, l in enumerate(lines) if l.startswith("Search:"))
        bottom = next(i for i, l in enumerate(lines) if l.startswith("type to search"))
        results = [l for l in lines[top + 1:bottom] if l.strip()]
    except StopIteration:
        results = []
    if len(results) == 1 and results[0].startswith("▸ "):
        # The full menu path matched exactly one destination; its display
        # title may differ from the manifest (e.g. "Process 07" renamed).
        found = True
        rec["display_path"] = results[0][2:].strip()
    for _ in range(0 if found else 25):
        sel = [l[2:].strip() for l in screen.splitlines() if l.startswith("▸ ")]
        if sel and (sel[0] == want or (sel[0].endswith("…") and want.startswith(sel[0][:-1]))):
            found = True
            break
        pane.keys("Down")
        time.sleep(0.25)
        nxt = pane.cap()
        if nxt == screen:
            break
        screen = nxt
    unwired = (wired or {}).get(rec["id"], {})
    if not found and unwired.get("status") == "under_construction":
        # Hidden on purpose until it is wired; the cause carries over so the
        # regenerated wired.json still says who owns it and why.
        rec.update(reached=False, state="HIDDEN", detail=unwired["cause"])
        pane.keys("Escape")
        return rec
    if not found and rec["id"] in EXTREME_HIDDEN:
        # Not offered in Extreme on purpose: ULTRA is local, so there is no
        # cloud entitlement to request.
        rec.update(reached=False, state="HIDDEN", detail="not offered in Marshal: ULTRA is local")
        pane.keys("Escape")
        return rec
    if not found:
        rec.update(reached=False, state="UNREACHABLE", detail="palette did not list the exact path")
        with open(os.path.join(outdir, rec["id"] + "-palette.txt"), "w") as fh:
            fh.write(screen)
        pane.keys("Escape")
        return rec
    pane.keys("Enter")
    time.sleep(1.2)
    pre = pane.cap()
    with open(os.path.join(outdir, rec["id"] + "-pre.txt"), "w") as fh:
        fh.write(pre)
    rec["reached"] = True
    detail_lines = [l.split("│", 1)[1].strip() for l in pre.splitlines() if "│" in l and l.split("│", 1)[1].strip()]
    rec["pane"] = " | ".join(detail_lines[:6])[:600]
    pane.keys("Enter")
    # Poll instead of one late capture: the background refresh can write
    # "refreshed" over the action's own status line within the wait, and
    # the first status the action wrote is the one that says what happened.
    seen = ""
    for _ in range(6):
        time.sleep(0.3)
        post = pane.cap()
        line = status_line(post)
        if not seen and line and line not in STATUS_NOISE:
            seen = line
    with open(os.path.join(outdir, rec["id"] + "-post.txt"), "w") as fh:
        fh.write(post)
    st = status_line(post)
    if seen and (not st or st in STATUS_NOISE):
        st = seen
    ov = overlay(post)
    rec["status_line"] = st
    if not is_nav(post):
        rec.update(state="ERROR", detail="screen left the advanced menu after Enter: " + post.splitlines()[0][:200] if post else "empty screen (crash?)")
    elif ov == "form":
        fields = [l.strip() for l in post.splitlines() if re.match(r"^\s*[▸ ] .*:", l) and "*" in l or l.strip().startswith("▸ ") and ":" in l]
        rec.update(state="FORM", detail="; ".join(fields)[:400])
    elif ov == "confirm":
        rec.update(state="CONFIRM", detail=st)
    elif ov == "result":
        result = [l.split("Result", 1)[1].strip() for l in post.splitlines() if l.startswith("Result ") and l.split("Result", 1)[1].strip()]
        verdict = result[0] if result else ""
        # A backend fault rendered as a verdict is still a fault.
        state = "ERROR" if re.search(r"invalid input|unsupported|panic|\berror\b", verdict.lower()) else "RESULT"
        rec.update(state=state, detail=verdict[:400])
    elif st in ("", "reading canonical state…", "refreshing…") and unavailable_reason(post):
        # The refusal reached the detail pane before any status line did.
        rec.update(state="DISABLED", detail=unavailable_reason(post))
    elif st and st not in ("reading canonical state…", "refreshing…"):
        low = st.lower()
        if re.search(r"\berror\b|\bfailed\b|panic|unexpected|invalid input|unsupported", low):
            rec.update(state="ERROR", detail=st)
        else:
            reason = unavailable_reason(post) or unavailable_reason(pre)
            if reason and (st in STATUS_NOISE or st.endswith("…")):
                st = reason
            rec.update(state="DISABLED", detail=st)
    elif normalise(post) == normalise(pre):
        rec.update(state="DEAD", detail="Enter produced no visible change")
    else:
        rec.update(state="CHANGED", detail="screen changed without form/confirm/status")
    return rec


# --deep: after the walk opened an action, probe it further. It still never
# selects Proceed: Enter is only pressed while a typed form is on screen and
# no confirmation is, and every confirmation is left with Esc.
FORM_MARK = "Type to edit  Tab/Shift+Tab"
CONFIRM_MARK = "── Confirm:"
# Bad input: a slash and a q test that keys reach the field, not the palette
# or quit; the rest is something no id, number or enum accepts.
BAD_INPUT = "zz/q?bad id"
PLAUSIBLE = "e2e-nonexistent-1"
PANIC_RE = re.compile(r"panic:|goroutine \d+ \[|runtime error|nil pointer")


def form_on(screen):
    return FORM_MARK in screen and CONFIRM_MARK not in screen


def settle(pane, wait=0.5):
    time.sleep(wait)
    return pane.cap()


def blocked_line(screen):
    for line in screen.splitlines():
        text = line.split("│", 1)[1].strip() if "│" in line else line.strip()
        if text.startswith("BLOCKED: "):
            return text[len("BLOCKED: "):]
    return ""


def form_submit(pane, typed):
    """Walk the open form with Enter, typing `typed` into each field first.

    Returns (outcome, text): outcome is REFUSED (the form kept a BLOCKED
    line), CONFIRM, RESULT, or LEFT (the form closed into something else).
    Enter is pressed only while the form, and no confirmation, is on screen.
    """
    screen = pane.cap()
    for _ in range(12):
        if not form_on(screen):
            break
        if typed is not None:
            pane.keys("C-u")
            if typed:
                pane.lit(typed)
            time.sleep(0.3)
        screen = settle(pane, 0.3)
        if not form_on(screen):
            break
        pane.keys("Enter")
        screen = settle(pane, 0.8)
        if form_on(screen) and blocked_line(screen):
            return "REFUSED", blocked_line(screen), screen
    # A confirmation may still be reading its target.
    for _ in range(10):
        if "Reading the canonical target" not in screen:
            break
        screen = settle(pane, 0.5)
    ov = overlay(screen)
    if ov == "result":
        res = [l.split("Result", 1)[1].strip() for l in screen.splitlines() if l.startswith("Result ") and l.split("Result", 1)[1].strip()]
        det = [l.split("│", 1)[-1].strip() for l in screen.splitlines() if "Detail" in l]
        return "RESULT", ((res[0] if res else "") + " " + (det[0] if det else "")).strip()[:300], screen
    if ov == "confirm":
        return "CONFIRM", status_line(screen), screen
    if form_on(screen):
        return "STUCK", status_line(screen), screen
    return "LEFT", status_line(screen) or (screen.splitlines()[0][:120] if screen else ""), screen


def esc_back(pane):
    """Esc until the menu shows no overlay; returns (clean, presses, status)."""
    status = ""
    for n in range(1, 5):
        pane.keys("Escape")
        screen = settle(pane, 0.6)
        st = status_line(screen)
        if st and st not in STATUS_NOISE and not status:
            status = st
        if is_nav(screen) and not overlay(screen):
            return True, n, status, screen
    return False, 4, status, screen


def deep_probe(pane, rec, outdir):
    d = {}
    post = pane.cap()
    pre_path = os.path.join(outdir, rec["id"] + "-pre.txt")
    pre = open(pre_path).read() if os.path.exists(pre_path) else ""
    d["not_run_yet"] = "Not run yet" in pre
    state = rec.get("state")
    if state == "FORM":
        d["required_fields"] = "*:" in post
        # Three passes, each on a freshly opened form: empty, bad input, and a
        # well-formed value nothing has (the target lookup must refuse it or a
        # confirmation must name it; Esc leaves that confirmation).
        for i, (key, typed) in enumerate((("empty", ""), ("bad", BAD_INPUT), ("plausible", PLAUSIBLE))):
            if i:
                esc_back(pane)
                again = walk(pane, {"spec_id": rec["id"], "type": rec["type"], "menu_path": ROOT + rec["path"]}, outdir)
                if again.get("state") != "FORM":
                    d[key] = "could not reopen form: %s" % again.get("state")
                    continue
            out, text, scr = form_submit(pane, typed)
            d[key] = out + (": " + text if text else "")
            if "Search:" in scr and "Enter go" in scr:
                d["palette_leak"] = True
            with open(os.path.join(outdir, "%s-%s.txt" % (rec["id"], key)), "w") as fh:
                fh.write(scr)
            if out == "CONFIRM":
                d[key + "_cancel"] = esc_back(pane)[2]
    elif state == "CONFIRM":
        scr = pane.cap()
        d["confirm_text"] = " | ".join(l.strip() for l in scr.splitlines() if CONFIRM_MARK in l)[:200]
        d["destructive_ack"] = "press 'y' to acknowledge" in scr
        d["default_cancel"] = "▸Cancel" in scr
    ok, n, st, scr = esc_back(pane)
    d["esc"] = ("clean" if ok else "STUCK") + ("" if n == 1 else " (%d Esc)" % n)
    if st:
        d["esc_status"] = st
    if PANIC_RE.search(scr) or PANIC_RE.search(post):
        d["panic"] = True
    # typed text must not land in a search box once back on the menu
    pane.lit("x")
    scr = settle(pane, 0.4)
    d["stray_search"] = "Search:" in scr
    if overlay(scr):
        pane.keys("Escape")
    return d


# Wired status. The walk is the source of truth: manifest.json marks 808 of
# 810 specs BOUND, which says nothing about whether Enter does anything.
WIRED_SCHEMA = "marshal.tui.wired.v1"
# Bugs land with the audit fixes; unwired features with Community features
# built by Extreme (private-docs/tui-v3-board.md "Phasing", roadmap B3).
PHASE_BUG = "A5"
PHASE_UNWIRED = "B3"
# Status lines the walker can catch that are not the refusal itself.
NOISE = {"", "refreshed", "refreshing…", "reading canonical state…"}
BROKEN = {"DEAD", "UNREACHABLE", "CHANGED"}


def read_text(path):
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def read_matrix(path):
    """Return {spec id: (state, detail)} from a *-menu-actions.md matrix."""
    rows = {}
    for line in read_text(path).splitlines():
        if not line.startswith("| CTUI-"):
            continue
        cells = [c.strip().replace("\\|", "|") for c in re.split(r"(?<!\\)\|", line.strip())[1:-1]]
        if len(cells) != 5:
            raise ValueError("%s: malformed row: %s" % (path, line.strip()))
        rows[cells[0]] = (cells[3], cells[4])
    return rows


def owner_of(cause, spec):
    """Name who owns the missing wiring: the component the refusal names."""
    m = re.match(r"([\w./-]+(?: service)?) owns\b", cause)
    if m:
        return m.group(1)
    m = re.search(r"`?\b([a-z][\w/-]*\.[A-Za-z][\w.]*\w|[A-Z]\w+\.[A-Z]\w+)`?", cause)
    if m:
        return m.group(1)
    return spec.get("process_association") or "unassigned"


def task_files(root, tasks):
    """Return {spec id: task file} for every spec id a task file names."""
    found = {}
    for dirpath, _, names in sorted(os.walk(os.path.join(root, tasks))):
        for name in sorted(names):
            if not name.endswith(".md"):
                continue
            path = os.path.join(dirpath, name)
            for spec in re.findall(r"CTUI-\d{4}", read_text(path)):
                found.setdefault(spec, os.path.relpath(path, root))
    return found


def classify(state, detail, spec):
    """Map one walk record to a wired entry (without spec_id)."""
    if state in BROKEN:
        raise ValueError("%s is %s: no menu entry may be broken" % (spec["spec_id"], state))
    cause = "" if detail in NOISE else detail
    if state == "HIDDEN" and spec["spec_id"] not in EXTREME_HIDDEN:
        # Under construction and hidden from the menu: the walk recorded the
        # cause wired.json gave it (classified below on the walk that saw it).
        if not cause:
            raise ValueError("%s is hidden with no cause; walk it with MARSHAL_TUI_SHOW_WIP=1" % spec["spec_id"])
        return {"status": "under_construction", "walk": state, "owner": owner_of(cause, spec),
                "cause": cause, "phase": PHASE_BUG if cause.startswith("bug: ") else PHASE_UNWIRED}
    if state in ("RESULT", "BLOCKED") or (state == "DISABLED" and cause.startswith("cannot start ")):
        # "cannot start" is a precondition refusal: the action is wired and
        # waits for state (a goal, a task), it is not under construction.
        return {"status": "blocked_by_state", "walk": state, "cause": cause}
    if state == "DISABLED":
        if not cause:
            # Every refusal names its reason (status line or the detail
            # pane's UNAVAILABLE text); an empty one is a walk that missed it,
            # and recording it would put an unexplained 🚧 in the menu.
            raise ValueError("%s was refused and the walk recorded no reason; re-walk it" % spec["spec_id"])
        return {"status": "under_construction", "walk": state, "owner": owner_of(cause, spec),
                "cause": cause, "phase": PHASE_UNWIRED}
    if state == "ERROR":
        cause = "bug: " + (cause or "the action failed")
        return {"status": "under_construction", "walk": state, "owner": owner_of(cause, spec),
                "cause": cause, "phase": PHASE_BUG}
    return {"status": "live", "walk": state}


def build_wired(root, matrix, manifest, tasks):
    walk = read_matrix(os.path.join(root, matrix))
    named = task_files(root, tasks)
    specs = json.loads(read_text(os.path.join(root, manifest)))["specs"]
    known = {s["spec_id"] for s in specs}
    stray = sorted(set(walk) - known)
    if stray:
        raise ValueError("matrix names specs the manifest does not: " + ", ".join(stray))
    out = []
    for spec in specs:
        entry = {"spec_id": spec["spec_id"]}
        if spec["spec_id"] in walk:
            entry.update(classify(*walk[spec["spec_id"]], spec))
        else:
            entry["status"] = "live"
        if entry["status"] == "under_construction":
            entry["task"] = named.get(spec["spec_id"], "")
        out.append(entry)
    return {"schema": WIRED_SCHEMA, "source": matrix, "specs": out}


def dump_wired(wired):
    # One spec per line, so a regenerated file diffs by spec.
    lines = ["{", '  "schema": %s,' % json.dumps(wired["schema"]),
             '  "source": %s,' % json.dumps(wired["source"]), '  "specs": [']
    lines += ["    " + json.dumps(e, ensure_ascii=False) + ("," if i < len(wired["specs"]) - 1 else "")
              for i, e in enumerate(wired["specs"])]
    return "\n".join(lines + ["  ]", "}"]) + "\n"


def regressions(root, old, new):
    """Specs live in old and under construction in new with no task file saying so."""
    before = {e["spec_id"]: e["status"] for e in old["specs"]}
    bad = []
    for e in new["specs"]:
        if before.get(e["spec_id"]) != "live" or e["status"] != "under_construction":
            continue
        task = e.get("task", "")
        path = os.path.join(root, task)
        if not task or not os.path.isfile(path) or e["spec_id"] not in read_text(path):
            bad.append("%s regressed from live to under construction (%s) and no task file names it"
                       % (e["spec_id"], e.get("cause", "")))
    return bad


def wired_main(argv):
    ap = argparse.ArgumentParser(prog="tui_walk.py wired")
    ap.add_argument("--matrix", help="matrix to generate from (default: the source the --out file names)")
    ap.add_argument("--manifest", default="internal/tui/manifest.json")
    ap.add_argument("--tasks", default="tasks")
    ap.add_argument("--out", default="internal/tui/wired.json")
    ap.add_argument("--root", default=".")
    ap.add_argument("--check", action="store_true", help="fail when --out is stale instead of writing it")
    ap.add_argument("--base", help="previous wired.json to check for regressions")
    args = ap.parse_args(argv)
    out = os.path.join(args.root, args.out)
    matrix = args.matrix
    if not matrix:
        matrix = json.loads(read_text(out))["source"]
    try:
        wired = build_wired(args.root, matrix, args.manifest, args.tasks)
    except ValueError as exc:
        print("wired: %s" % exc, file=sys.stderr)
        return 1
    text = dump_wired(wired)
    if args.check:
        if read_text(out) != text:
            print("wired: %s is stale; run tui_walk.py wired --matrix %s" % (args.out, matrix), file=sys.stderr)
            return 1
    else:
        with open(out, "w", encoding="utf-8") as fh:
            fh.write(text)
    if args.base:
        bad = regressions(args.root, json.loads(read_text(args.base)), wired)
        for line in bad:
            print("wired: " + line, file=sys.stderr)
        if bad:
            return 1
    return 0


def main():
    if sys.argv[1:2] == ["wired"]:
        return wired_main(sys.argv[2:])
    ap = argparse.ArgumentParser()
    ap.add_argument("--target", required=True)
    ap.add_argument("--manifest", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--types", default=",".join(ACTION_TYPES))
    ap.add_argument("--only", default="")
    ap.add_argument("--deep", action="store_true", help="also probe validation, confirmation cancel and Esc (never Proceed)")
    ap.add_argument("--wired", default="", help="wired.json the TUI under test embeds; its under-construction entries are hidden")
    args = ap.parse_args()
    wired = {}
    if args.wired:
        wired = {e["spec_id"]: e for e in json.load(open(args.wired))["specs"]}
    os.makedirs(args.out, exist_ok=True)
    specs = json.load(open(args.manifest))["specs"]
    types = set(args.types.split(","))
    only = set(filter(None, args.only.split(",")))
    pane = Pane(args.target)
    for node in specs:
        if node["type"] not in types or (only and node["spec_id"] not in only):
            continue
        start = time.time()
        try:
            rec = walk(pane, node, args.out, wired)
            if args.deep and rec.get("reached"):
                rec["deep"] = deep_probe(pane, rec, args.out)
        except Exception as exc:  # noqa: BLE001
            rec = {"id": node["spec_id"], "state": "ERROR", "detail": "walker exception: %s" % exc}
        rec["seconds"] = round(time.time() - start, 1)
        print(json.dumps(rec, ensure_ascii=False), flush=True)
    reset(pane)
    return 0


if __name__ == "__main__":
    sys.exit(main())
