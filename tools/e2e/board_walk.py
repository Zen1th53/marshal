#!/usr/bin/env python3
"""Walk the Board (TUI v3) of a running MARSHAL TUI through tmux.

TUI3-0012: the Board renders at 100/120/170/200 columns, the help overlay
opens and closes, the focus keys move between tasks, lanes and the inbox,
and the inspector opens on a task and closes again. The walk only presses
navigation keys: it never opens an inbox confirmation, never types into the
composer and never decides anything.

Usage:
  board_walk.py --target SESSION:WINDOW --out DIR [--sizes 100x30,120x40,...]

Prints one JSON object per check on stdout, as flow_driver.py does:
  {"id": "...", "status": "PASS|FAIL|SKIP|INFO", "seconds": n, "evidence": "..."}
and writes DIR/board-<check>.txt captures. Requires TMUX_TMPDIR to point at
the isolated tmux server that owns SESSION. When the window shows no Board
(no Marshal run is open) every check is SKIP.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time

SIZES = "100x30,120x40,170x45,200x60"
PANIC = re.compile(r"panic:|runtime error|nil pointer|goroutine \d+ \[", re.I)
FOCUS = {"tasks": "TASKS", "lanes": "LANES", "inbox": "INBOX"}


def tmux(*args):
    return subprocess.run(["tmux", *args], capture_output=True, text=True, check=False)


def emit(cid, status, seconds, evidence):
    print(json.dumps({"id": cid, "status": status, "seconds": round(seconds, 1), "evidence": evidence[:400]}), flush=True)


# ---------------------------------------------------------------- screens
# Pure functions over a captured screen, unit-tested against the goldens.

def lines(screen):
    return screen.rstrip("\n").split("\n") if screen else []


def board_shown(screen):
    """The Board's header and at least one of its panes are on screen."""
    ls = lines(screen)
    if not ls or not ls[0].lstrip().startswith(("MARSHAL", "Marshal")):
        return False
    body = "\n".join(ls[1:])
    return bool(re.search(r"(^|[\s│>])(TASKS|LANES|INBOX)\b", body, re.M))


def footer_ok(screen):
    """The footer still shows the help key or a status line, never nothing."""
    tail = [l for l in lines(screen) if l.strip()]
    return bool(tail) and any(mark in tail[-1] for mark in ("j/k", "help", "›", "esc"))


def focused(screen):
    """The pane the Board has focused: its title is drawn '> NAME'."""
    for name, title in FOCUS.items():
        if re.search(r"(^|[\s│])> %s\b" % title, screen, re.M):
            return name
    return ""


def help_open(screen):
    return bool(re.search(r"\bKEYS\b", screen)) and "Esc or ? closes" in screen


def inspector_open(screen):
    return "IDENTITY" in screen and "AUTHORITY" in screen


def nav_open(screen):
    """The Ctrl+N navigation owns the screen: its section bar is row one."""
    first = lines(screen)[0] if screen else ""
    return "Home" in first and "Control" in first and "System" in first


def widest(screen):
    return max((len(l.rstrip()) for l in lines(screen)), default=0)


# ------------------------------------------------------------------- walk

class Walk:
    def __init__(self, target, out):
        self.target = target
        self.out = out
        self.window = target

    def cap(self):
        return tmux("capture-pane", "-p", "-t", self.target).stdout

    def cap_joined(self):
        """The screen with wrapped rows joined: a row the Board painted wider
        than its pane shows here as one over-long line."""
        return tmux("capture-pane", "-p", "-J", "-t", self.target).stdout

    def keys(self, *k):
        tmux("send-keys", "-t", self.target, *k)

    def save(self, name, screen):
        with open(os.path.join(self.out, "board-%s.txt" % name), "w", encoding="utf-8") as fh:
            fh.write(screen)

    def pane_size(self):
        out = tmux("display-message", "-p", "-t", self.target, "#{pane_width} #{pane_height}").stdout.split()
        return (int(out[0]), int(out[1])) if len(out) == 2 else (0, 0)

    def window_size(self):
        out = tmux("display-message", "-p", "-t", self.target, "#{window_width} #{window_height}").stdout.split()
        return (int(out[0]), int(out[1])) if len(out) == 2 else (0, 0)

    def close_overlays(self):
        for _ in range(4):
            screen = self.cap()
            if help_open(screen) or inspector_open(screen):
                self.keys("Escape")
            elif nav_open(screen):
                self.keys("q")
            else:
                return
            time.sleep(0.4)

    def wait(self, ok, seconds=6.0):
        deadline = time.time() + seconds
        screen = self.cap()
        while not ok(screen) and time.time() < deadline:
            time.sleep(0.2)
            screen = self.cap()
        return screen, ok(screen)

    def resize_board(self, cols, rows):
        """Size the window so the Board's own pane is cols wide.

        The docked Marshal chat shares the window, so the window grows by
        whatever the chat takes, measured, not assumed."""
        tmux("resize-window", "-t", self.target, "-x", str(cols), "-y", str(rows))
        self._wait_pane_size(cols, timeout=2.0)
        for _ in range(3):
            pw, _ = self.pane_size()
            ww, _ = self.window_size()
            if pw == cols or pw <= 0:
                break
            tmux("resize-window", "-t", self.target, "-x", str(ww + cols - pw), "-y", str(rows))
            self._wait_pane_size(cols, timeout=2.0)
        return self.pane_size()

    def _wait_pane_size(self, cols, timeout=2.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            pw, _ = self.pane_size()
            if pw == cols:
                return
            time.sleep(0.05)


def run(target, out, sizes):
    os.makedirs(out, exist_ok=True)
    w = Walk(target, out)
    t0 = time.time()
    screen = w.cap()
    if not board_shown(screen):
        w.save("none", screen)
        emit("BW00-board-on-screen", "SKIP", time.time() - t0, "no Board on %s (no Marshal run open)" % target)
        return 0
    emit("BW00-board-on-screen", "PASS", time.time() - t0, screen.split("\n", 1)[0].strip()[:200])
    original = w.window_size()
    fails = 0
    try:
        for size in sizes:
            t = time.time()
            cols, rows = (int(x) for x in size.split("x"))
            pw, ph = w.resize_board(cols, rows)
            screen, ok = w.wait(lambda s: board_shown(s) and footer_ok(s))
            w.save(size, screen)
            bad = []
            if not ok:
                bad.append("Board header/footer missing")
            if PANIC.search(screen):
                bad.append("panic text: " + PANIC.search(screen).group(0))
            joined = w.cap_joined()
            if widest(joined) > pw:
                bad.append("a row is %d cells in a %d-cell pane" % (widest(joined), pw))
            status = "FAIL" if bad else "PASS"
            fails += bool(bad)
            emit("BW1-render-%s" % size, status, time.time() - t,
                 "; ".join(bad) or "Board pane %dx%d: header, footer, panes" % (pw, ph))
        # Navigation at the widest size that still shows every pane.
        w.resize_board(*(int(x) for x in sizes[min(2, len(sizes) - 1)].split("x")))
        checks = [
            ("BW20-help-opens", ["?"], help_open, "help overlay lists the keys"),
            ("BW21-help-closes", ["Escape"], lambda s: not help_open(s) and board_shown(s), "Esc closes it"),
            ("BW22-focus-tasks", ["t"], lambda s: focused(s) == "tasks", "t focuses the tasks"),
            ("BW23-inspector-opens", ["j", "Enter"], inspector_open, "Enter inspects the task"),
            ("BW24-inspector-closes", ["Escape"], lambda s: not inspector_open(s) and board_shown(s), "Esc closes it"),
            ("BW25-focus-lanes", ["l"], lambda s: focused(s) == "lanes", "l focuses the lanes"),
            ("BW26-focus-inbox", ["i"], lambda s: focused(s) == "inbox", "i focuses the inbox"),
        ]
        for cid, keys, ok, what in checks:
            t = time.time()
            for k in keys:
                w.keys(k)
                time.sleep(0.3)
            screen, passed = w.wait(ok)
            w.save(cid, screen)
            if PANIC.search(screen):
                passed = False
            fails += not passed
            emit(cid, "PASS" if passed else "FAIL", time.time() - t,
                 what if passed else "%s: not seen; focus=%r" % (what, focused(screen)))
    finally:
        # Leave the Board as it was found: tasks focused, nothing open. Esc
        # only closes what is open; on the bare Board it does nothing.
        w.close_overlays()
        w.keys("t")
        if original[0] > 0:
            tmux("resize-window", "-t", target, "-x", str(original[0]), "-y", str(original[1]))
    return 1 if fails else 0


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--target", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--sizes", default=SIZES)
    a = ap.parse_args(argv)
    run(a.target, a.out, [s for s in a.sizes.split(",") if s])
    # Failures are records, not an exit status: the caller tallies them.
    return 0


if __name__ == "__main__":
    sys.exit(main())
