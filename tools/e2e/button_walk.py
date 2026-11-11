#!/usr/bin/env python3
"""Press every Board button of a running MARSHAL TUI through tmux.

Where board_walk.py checks that the Board renders and that its focus keys
work, this walk presses everything the Board, its views, the inspector, the
inbox and the header offer, by key and by real SGR mouse reports, and every
slash command once, and records what each one did:

  PASS  the expected screen change happened
  FAIL  something else happened: a crash or panic text, the key fell into
        the composer, an error, or the wrong screen
  DEAD  a label or key the screen offers did nothing at all
  SKIP  nothing to press in this session (no lanes, no inbox item)
  EXPECTED  by design, not a defect, with its category: an unbound letter
        on the bare Board starts composer text (owner decision 2026-11-06,
        "composer-by-design"). Not counted as FAIL

Usage:
  button_walk.py --target PANE --out DIR [--groups board,views,...]
                 [--commands-from internal/tui/workspace.go]

PANE is the Board's tmux pane (e.g. %0). Requires TMUX_TMPDIR to point at
the isolated tmux server that owns it. Prints one JSON object per check:
  {"id", "group", "status", "evidence"}
and writes DIR/<id>-before.txt / -after.txt captures (with -e colour).

It never decides anything: inbox actions are pressed only to open their
confirmation, which is then cancelled with Esc; write commands are typed
bare, without the arguments that would make them act, and the few that
act even bare (/quit, /exit, /update, /login, /logout) are only typed, not
entered. Run it only against a throwaway project.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time

PANIC = re.compile(r"panic:|runtime error|nil pointer|goroutine \d+ \[", re.I)
NOISE = [
    (re.compile(r"\b\d\d:\d\d:\d\d\b"), "hh:mm:ss"),
    (re.compile(r"\b\d\d:\d\d\b"), "mm:ss"),
    (re.compile(r"host \d+°C"), "host T"),
    (re.compile(r"\d+(\.\d+)? ?°C"), "T"),
    (re.compile(r"read [^·│]*ago"), "read"),
    (re.compile(r"[⣾⣽⣻⢿⡿⣟⣯⣷◐◓◑◒]"), "*"),
    (re.compile(r"[▁▂▃▄▅▆▇█]+"), "spark"),
    (re.compile(r"\d+(\.\d+)? ?(GHz|GiB|MiB|%|s ago|ms)"), "N"),
    (re.compile(r"load [\d. ]+"), "load"),
    (re.compile(r"up \d+h\d+m"), "up"),
]
# Commands that act even when typed bare: typed, never entered.
NO_ENTER = {"/quit", "/exit", "/update", "/login", "/logout"}


def tmux(*args):
    return subprocess.run(["tmux", *args], capture_output=True, text=True, check=False)


def norm(screen):
    for rx, sub in NOISE:
        screen = rx.sub(sub, screen)
    return "\n".join(l.rstrip() for l in screen.splitlines())


def emit(cid, group, status, evidence, category=None):
    rec = {"id": cid, "group": group, "status": status, "evidence": evidence[:500]}
    if category:
        rec["category"] = category
    print(json.dumps(rec), flush=True)


class Board:
    def __init__(self, pane, out):
        self.pane = pane
        self.out = out

    def cap(self, color=False):
        args = ["capture-pane", "-p", "-t", self.pane]
        if color:
            args.insert(2, "-e")
        return tmux(*args).stdout

    def keys(self, *k):
        tmux("send-keys", "-t", self.pane, *k)

    def lit(self, text):
        tmux("send-keys", "-t", self.pane, "-l", text)

    def click(self, col, row):
        """A left press and release at 1-based pane cell (col, row), as a
        terminal in SGR mouse mode (1006) reports it."""
        self.lit("\x1b[<0;%d;%dM" % (col, row))
        time.sleep(0.05)
        self.lit("\x1b[<0;%d;%dm" % (col, row))

    def active_pane(self):
        return tmux("display-message", "-p", "-t", self.pane, "#{pane_id}:#{pane_active}").stdout.strip()

    def window_active(self):
        win = tmux("display-message", "-p", "-t", self.pane, "#{window_id}").stdout.strip()
        return tmux("display-message", "-p", "-t", win, "#{pane_id}").stdout.strip()

    def save(self, name, text):
        with open(os.path.join(self.out, name), "w", encoding="utf-8") as fh:
            fh.write(text)

    def settle(self, seconds=0.9):
        time.sleep(seconds)

    def footer(self):
        ls = [l for l in self.cap().splitlines() if l.strip()]
        return ls[-1] if ls else ""

    def composer_text(self):
        """What the composer holds, '' when empty. The Board's bottom row is
        the composer while it holds text (no '›' mark, no key hints), and
        the status line with its key hints otherwise."""
        last = self.footer()
        if last.lstrip().startswith("›") or re.search(r"j/k (move|select)|esc board|tell the marshal", last):
            return ""
        # Text that is not a command is shown as a message to the Marshal.
        return re.sub(r"^\s*to the Marshal ›\s?", "", last).strip()

    def saying(self):
        """Whether the bottom row is the message line s opens."""
        return self.footer().lstrip().startswith("to the Marshal ›")

    def clear_composer(self):
        """Empty the composer. Ctrl+C clears a non-empty composer and does
        nothing else; it is sent only then, because on an empty one it arms
        the exit."""
        if not self.composer_text() and not self.saying():
            return
        # Esc first closes a completion menu over the composer, and the
        # message line s opened.
        self.keys("Escape")
        time.sleep(0.3)
        if not self.composer_text() and not self.saying():
            return
        # Once only: two Ctrl+C in a row on an empty composer exit.
        self.keys("C-c")
        time.sleep(0.3)
        if "Press Ctrl+C again" in self.footer():
            self.keys("t")
            time.sleep(0.2)

    def home(self):
        """Back to the bare Board: tasks focused, nothing over it."""
        for _ in range(8):
            self.clear_composer()
            s = self.cap()
            first = s.splitlines()[0] if s else ""
            # The section bar abbreviates its titles on a narrow pane:
            # "[1Hom]  2Con" instead of "1 Home  2 Control".
            if ("Home" in first and "Control" in first) or re.search(r"\[?1 ?Hom.*2 ?Con", first):
                self.keys("q")
            elif ("Search:" in s and "Enter go" in s) or "Command Palette" in s:
                self.keys("Escape")
            elif " ANSWER " in s:
                self.keys("Escape")
            elif "Esc or ? closes" in s or "IDENTITY" in s or "Esc or Enter to dismiss" in s or re.search(r"Cancel\s+.?Proceed", s):
                self.keys("Escape")
            elif not first.lstrip().startswith("MARSHAL"):
                self.keys("Escape")
            elif not re.search(r"(^|\s)> TASKS\b|^TASKS\b", s, re.M) and re.search(r"^(HOST|MEMORY|PROVIDERS|VERIFY|SECURITY|PLAN|ARCHITECTURE)\b|^Architecture ·", s, re.M):
                self.keys("1")
            else:
                self.clear_composer()
                self.keys("t")
                time.sleep(0.3)
                return True
            time.sleep(0.5)
        return False


def classify(before, after, ok, leak_char=None, composer=""):
    if PANIC.search(after):
        return "FAIL", "panic text: " + PANIC.search(after).group(0)
    if leak_char and composer and composer.strip() == leak_char:
        return "FAIL", "key fell into the composer: %r" % composer
    if ok(before, after):
        return "PASS", ""
    if norm(before) == norm(after):
        return "DEAD", "screen unchanged"
    return "FAIL", "unexpected screen"


def diff_hint(before, after):
    b, a = norm(before).splitlines(), norm(after).splitlines()
    for i in range(max(len(a), len(b))):
        x = b[i] if i < len(b) else ""
        y = a[i] if i < len(a) else ""
        if x != y:
            return "row %d: %r -> %r" % (i, x.strip()[:120], y.strip()[:120])
    return ""


# ------------------------------------------------------------ predicates

def has(rx):
    r = re.compile(rx, re.M)
    return lambda b, a: bool(r.search(a))


def gone(rx):
    r = re.compile(rx, re.M)
    return lambda b, a: bool(r.search(b)) and not r.search(a)


def selected(screen):
    m = re.search(r"^\s*▸\s*(T-\d+)", screen, re.M)
    return m.group(1) if m else ""


def moved(b, a):
    return selected(b) != "" and selected(a) != "" and selected(b) != selected(a)


def focus(name):
    return has(r"(^|[\s│])> %s\b" % name)


def view(name):
    return has(r"^%s(?:\b|\s|$)" % name)


def first_line(rx):
    r = re.compile(rx)
    return lambda b, a: bool(r.search(a.splitlines()[0] if a else ""))


# ------------------------------------------------------------------ walk

def step(bd, cid, group, keys, ok, what, prep=None, leak=None, literal=False, wait=0.9, after_fn=None, act=None, expected=None, category="composer-by-design"):
    if prep:
        prep()
    before = bd.cap()
    before_c = bd.cap(True)
    if act:
        act()
    for k in keys:
        if literal:
            bd.lit(k)
        else:
            bd.keys(k)
        time.sleep(0.15)
    bd.settle(wait)
    after = bd.cap()
    bd.save(cid + "-before.ans", before_c)
    bd.save(cid + "-after.ans", bd.cap(True))
    comp = bd.composer_text()
    status, why = classify(before, after, ok, leak, comp)
    if expected and status != "PASS" and expected(before, after, comp):
        # The behaviour is the intended one (see EXPECTED in the module doc).
        status = "EXPECTED"
    extra = after_fn(after) if after_fn else ""
    ev = what if status == "PASS" else "%s; %s %s" % (what, why, diff_hint(before, after))
    if extra:
        ev += " · " + extra
    emit(cid, group, status, ev, category=category if status == "EXPECTED" else None)
    return status, after


def walk_board(bd):
    g = "board"
    bd.home()
    step(bd, "BK-j", g, ["j"], moved, "j moves the task selection down")
    step(bd, "BK-k", g, ["k"], moved, "k moves it up")
    step(bd, "BK-Down", g, ["Down"], moved, "Down moves down")
    step(bd, "BK-Up", g, ["Up"], moved, "Up moves up")
    step(bd, "BK-l", g, ["l"], focus("LANES"), "l focuses the lanes")
    step(bd, "BK-i", g, ["i"], focus("INBOX"), "i focuses the inbox")
    step(bd, "BK-t", g, ["t"], focus("TASKS"), "t focuses the tasks")
    step(bd, "BK-Tab", g, ["Tab"], lambda b, a: not focus("TASKS")(b, a) and bool(re.search(r"> (LANES|INBOX)\b", a)), "Tab moves to the next pane")
    bd.home()
    step(bd, "BK-Enter", g, ["Enter"], has(r"IDENTITY"), "Enter inspects the selected task")
    step(bd, "BK-Esc-inspector", g, ["Escape"], gone(r"IDENTITY"), "Esc closes the inspector")
    bd.home()
    step(bd, "BK-Esc-bare", g, ["Escape"], lambda b, a: "Home" not in a.splitlines()[0], "Esc on the bare Board does nothing")
    bd.home()
    step(bd, "BK-help-open", g, ["?"], has(r"Esc or \? closes"), "? opens the help")
    step(bd, "BK-help-close", g, ["?"], gone(r"Esc or \? closes"), "? closes it again")
    bd.home()
    # The help must be open in the "before" capture, or gone() cannot see
    # it close.
    step(bd, "BK-help-anykey", g, ["x"], gone(r"Esc or \? closes"), "a key closes the help", leak="x", prep=lambda: (bd.keys("?"), bd.settle()))
    bd.home()
    step(bd, "BK-Ctrl+P", g, ["C-p"], has(r"Command Palette"), "Ctrl+P opens the palette")
    bd.home()
    step(bd, "BK-slash", g, ["/"], lambda b, a: bd.composer_text().startswith("/"), "/ starts a command")
    bd.home()
    step(bd, "BK-d", g, ["d"], lambda b, a: norm(a) != norm(b) and bd.composer_text() != "d", "d opens the diff (inbox empty)", leak="d")
    bd.home()
    for k in ["e", "q", "x", "a", "A", "r", "o", "z"]:
        # Owner decision 2026-11-06: an unbound letter on the bare Board
        # starts a message or command in the composer (classic behaviour).
        step(bd, "BK-unbound-" + k, g, [k], lambda b, a, k=k: bd.composer_text() != k, "an unbound letter on the bare Board starts composer text (composer-by-design)", leak=k,
             expected=lambda b, a, comp, k=k: comp.strip() == k)
        bd.home()
    # m focuses the docked chat; F11 in the chat comes back.
    before = bd.window_active()
    bd.keys("m")
    time.sleep(1)
    now = bd.window_active()
    emit("BK-m", g, "PASS" if now != before and now != bd.pane else ("DEAD" if now == before else "FAIL"), "m moves tmux focus to the docked chat (%s -> %s)" % (before, now))
    # F11 goes through the client, as a terminal would send it, so the
    # dock's key table (which binds F11) applies; send-keys -t to the pane
    # would bypass it.
    client = tmux("list-clients", "-F", "#{client_tty}").stdout.split()
    if client:
        tmux("send-keys", "-K", "-c", client[0], "F11")
    else:
        tmux("send-keys", "-t", now, "F11")
    time.sleep(1)
    back = bd.window_active()
    emit("BK-F11-back", g, "PASS" if back == bd.pane else "FAIL", "F11 in the chat returns focus to the Board (%s)" % back)
    if back != bd.pane:
        tmux("select-pane", "-t", bd.pane)
    # The other switch keys, through the client as a terminal sends them:
    # F11 on the Board goes to the chat, Alt+Right from the chat comes
    # back, Alt+Left from the Board goes to the chat again.
    if client and now != before:
        for cid, key, want_chat, what in [("BK-F11-to-chat", "F11", True, "F11 on the Board moves focus to the chat"),
                                          ("BK-Alt-Right", "M-Right", False, "Alt+Right in the chat moves focus to the Board"),
                                          ("BK-Alt-Left", "M-Left", True, "Alt+Left on the Board moves focus to the chat"),
                                          ("BK-F11-toggle", "F11", False, "F11 in the chat toggles back to the Board")]:
            tmux("send-keys", "-K", "-c", client[0], key)
            time.sleep(0.8)
            got = bd.window_active()
            ok = (got != bd.pane) if want_chat else (got == bd.pane)
            emit(cid, g, "PASS" if ok else "FAIL", "%s (%s)" % (what, got))
        if bd.window_active() != bd.pane:
            tmux("select-pane", "-t", bd.pane)
    # s opens the message line to the Marshal; Esc closes it.
    bd.home()
    step(bd, "BK-s", g, ["s"], lambda b, a: bd.saying(), "s opens the message line to the Marshal")
    step(bd, "BK-s-esc", g, ["Escape"], lambda b, a: not bd.saying(), "Esc closes the message line")
    bd.home()


def walk_views(bd):
    g = "views"
    views = [("2", "HOST"), ("h", "HOST"), ("3", "MEMORY"), ("4", "PROVIDERS"), ("5", "VERIFY"), ("6", "SECURITY"), ("7", "PLAN"), ("8", "Architecture ·"), ("b", "Architecture ·")]
    for key, name in views:
        bd.home()
        step(bd, "V-%s-%s" % (key, name.lower()), g, [key], view(name), "%s opens the %s view" % (key, name))
        step(bd, "V-%s-esc" % key, g, ["Escape"], lambda b, a: bool(re.search(r"(^|\s)> TASKS\b", a, re.M)), "Esc returns to the Board")
        bd.home()
        bd.keys(key)
        bd.settle()
        step(bd, "V-%s-1" % key, g, ["1"], lambda b, a: bool(re.search(r"(^|\s)> TASKS\b", a, re.M)), "1 returns to the Board")
    # Each view's own keys, from the help: they open a confirmation or
    # answer with a refusal, never decide at once.
    per_view = {
        "3": ["a", "r", "i", "PageDown", "PageUp", "j", "k"],
        "4": ["v", "r", "j", "k"],
        "5": ["x", "j", "k"],
        "6": ["a", "d", "j", "k"],
        "7": ["j", "k", "Enter"],
        "8": ["o"],
        # The prototype's c (clean build cache) and s (simulate heavy
        # build) were never built and are not offered as hints.
        "2": ["j", "k"],
    }
    for vk, keys in per_view.items():
        for k in keys:
            bd.home()
            bd.keys(vk)
            bd.settle()
            st, after = step(bd, "VK-%s-%s" % (vk, k), g, [k], lambda b, a: norm(a) != norm(b), "view %s key %s answers" % (vk, k), leak=k if len(k) == 1 else None)
            bd.keys("Escape")
            time.sleep(0.4)
    # Switching views directly, view to view.
    bd.home()
    bd.keys("2")
    bd.settle()
    for key, name in views[2:]:
        # b after 8 would press the open view's own key, which returns to
        # the Board as every view's own key does: come from another view.
        prep = (lambda: (bd.keys("2"), bd.settle())) if key == "b" else None
        step(bd, "VV-to-%s" % key, g, [key], view(name), "%s from another view opens %s" % (key, name), prep=prep)
    bd.home()


def walk_inspector(bd):
    g = "inspector"
    bd.home()
    # Start from the top task: an earlier step (the Plan view's Enter)
    # may have left the last task selected, where j has nowhere to go.
    for _ in range(20):
        bd.keys("k")
    bd.settle(0.5)
    bd.keys("Enter")
    bd.settle()
    step(bd, "I-e", g, ["e"], lambda b, a: norm(a) != norm(b), "e toggles all events")
    step(bd, "I-j", g, ["j"], lambda b, a: norm(a) != norm(b) and "IDENTITY" in a, "j moves to the next task in the inspector")
    step(bd, "I-k", g, ["k"], lambda b, a: norm(a) != norm(b) and "IDENTITY" in a, "k moves back")
    step(bd, "I-Enter", g, ["Enter"], lambda b, a: "IDENTITY" in a, "Enter walks to a child task (none here: stays)")
    step(bd, "I-Backspace", g, ["BSpace"], gone(r"IDENTITY"), "Backspace at the top closes the inspector")
    for k in ["q", "a", "A", "d", "r", "x", "o", "i"]:
        bd.home()
        bd.keys("Enter")
        bd.settle()
        step(bd, "I-" + k, g, [k], lambda b, a, k=k: bd.composer_text() != k, "inspector key %s does not fall into the composer" % k, leak=k)
    bd.home()


def walk_inbox(bd):
    g = "inbox"
    bd.home()
    s = bd.cap()
    if re.search(r"INBOX\s+empty", s):
        emit("IB-any", g, "SKIP", "inbox empty in this session")
        return
    bd.keys("i")
    bd.settle()
    step(bd, "IB-j", g, ["j"], lambda b, a: norm(a) != norm(b), "j moves in the inbox")
    step(bd, "IB-k", g, ["k"], lambda b, a: norm(a) != norm(b), "k moves back")
    for k in ["a", "A", "d", "r", "x", "i", "Enter"]:
        bd.home()
        bd.keys("i")
        bd.settle()
        st, after = step(bd, "IB-" + k, g, [k], lambda b, a: norm(a) != norm(b), "inbox key %s opens its confirmation or detail" % k, leak=k if len(k) == 1 else None)
        bd.save("IB-%s-open.txt" % k, after)
        bd.keys("Escape")
        time.sleep(0.5)
        # The request must still be waiting: nothing was decided.
        still = not re.search(r"INBOX\s+empty", bd.cap())
        emit("IB-%s-undecided" % k, g, "PASS" if still else "FAIL", "after Esc the request still waits in the inbox")
    # x cancels the decision being confirmed.
    bd.home()
    bd.keys("i")
    bd.settle()
    bd.keys("a")
    bd.settle()
    step(bd, "IB-x-in-confirm", g, ["x"], gone(r"Enter to confirm"), "x cancels the confirmation in progress")
    still = not re.search(r"INBOX\s+empty", bd.cap())
    emit("IB-x-in-confirm-undecided", g, "PASS" if still else "FAIL", "after x the request still waits")
    # Mouse on the inbox buttons: a click opens that action's confirmation.
    for label in ["[a]", "[A]", "[d]", "[i]"]:
        bd.home()
        bd.keys("i")
        bd.settle()
        ls = bd.cap().splitlines()
        pos = [(y, l.find(label)) for y, l in enumerate(ls) if label in l and "allow" in l]
        if not pos:
            emit("IBM-" + label, g, "SKIP", "button %s not on screen" % label)
            continue
        y, x = pos[0]
        step(bd, "IBM-" + label.strip("[]"), g, [], lambda b, a: norm(a) != norm(b), "click on inbox button %s opens its confirmation" % label, act=lambda x=x, y=y: bd.click(x + 2, y + 1))
        bd.keys("Escape")
        time.sleep(0.5)
        still = not re.search(r"INBOX\s+empty", bd.cap())
        emit("IBM-%s-undecided" % label.strip("[]"), g, "PASS" if still else "FAIL", "after Esc the request still waits")
    bd.home()


def header_spans(screen):
    """Header chips by the text they show, with their 1-based columns."""
    first = screen.splitlines()[0] if screen else ""
    out = {}
    for name, rx in [("brand", r"MARSHAL"), ("run", r"run R-\w+|R-\w+"), ("state", r"DISPATCHING|[A-Z]{5,}(?= ·)"), ("done", r"\d+/\d+( done)?"),
                     ("lanes", r"\S \d+ lanes"), ("budget", r"budget"), ("inbox", r"inbox \d+|⚑\d+"), ("health", r"codex [●◐○×]|[●◐○×] [●◐○×]"),
                     ("arch", r"arch rev \d+"), ("step", r"step [A-Z]\d+"), ("host", r"host \d+"), ("clock", r"\d\d:\d\d:\d\d")]:
        m = re.search(rx, first)
        if m:
            out[name] = m.start() + 1 + 1
    return out


def walk_mouse(bd):
    g = "mouse"
    bd.home()
    s = bd.cap()
    spans = header_spans(s)
    expect = {
        "brand": focus("TASKS"), "run": focus("TASKS"), "state": focus("TASKS"), "done": focus("TASKS"),
        "lanes": focus("LANES"), "inbox": focus("INBOX"), "health": view("PROVIDERS"),
        "host": view("HOST"),
    }
    for name, col in spans.items():
        bd.home()
        if name in ("lanes", "inbox"):
            pass
        else:
            bd.keys("l")  # so a chip that focuses tasks visibly does something
            time.sleep(0.3)
        ok = expect.get(name, lambda b, a: norm(a) == norm(b))
        what = "click on header chip %s" % name + (" (read-only)" if name not in expect else "")
        st, _ = step(bd, "M-chip-" + name, g, [], ok, what, act=lambda col=col: bd.click(col, 1), wait=1.0)
    bd.home()
    # Task rows: one click selects, a second inspects.
    s = bd.cap()
    rows = [i + 1 for i, l in enumerate(s.splitlines()) if re.match(r"^\s*[▸ ]\s*T-\d+", l)]
    if len(rows) >= 2:
        target = rows[1]
        tid = re.search(r"T-\d+", s.splitlines()[target - 1]).group(0)
        step(bd, "M-task-select", g, [], lambda b, a: selected(a) == tid, "click on task row %s selects it" % tid, act=lambda: bd.click(10, target))
        step(bd, "M-task-inspect", g, [], has(r"IDENTITY"), "second click on it inspects it", act=lambda: bd.click(10, target))
        bd.home()
    # Footer hints: the prototype draws them as buttons.
    s = bd.cap()
    ls = s.splitlines()
    fy = max((i for i, l in enumerate(ls) if "j/k" in l), default=-1)
    if fy >= 0:
        line = ls[fy]
        for label, ok in [("? help", has(r"Esc or \? closes")), ("^N advanced", first_line(r"1 ?Hom")), ("^P palette", has(r"Command Palette")), ("i inbox", focus("INBOX")), ("l lanes", focus("LANES")), ("/ command", lambda b, a: bd.composer_text().startswith("/")), ("m marshal", lambda b, a: bd.window_active() != bd.pane), ("m/F11 chat", lambda b, a: bd.window_active() != bd.pane), ("s say", lambda b, a: bd.saying()), ("h host", view("HOST")), ("3 memory", view("MEMORY"))]:
            x = line.find(label.split()[0] + " " + label.split()[1]) if " " in label else line.find(label)
            if x < 0:
                x = line.find(label.split()[1])
                if x >= 0 and line[max(0, x - 2):x] == "▲ ":
                    # A host warning ("▲ host: …") replaced the hint: its
                    # word is not the hint's button.
                    emit("M-footer-" + label.split()[1], g, "SKIP", "host warning replaces the hint %r" % label)
                    continue
            if x < 0:
                continue
            bd.home()
            step(bd, "M-footer-" + label.split()[1], g, [], ok, "click on footer hint %r" % label, act=lambda x=x: bd.click(x + 1, fy + 1))
            if bd.window_active() != bd.pane:
                tmux("select-pane", "-t", bd.pane)
    # Wheel and right button: ignored, never typed.
    bd.home()
    step(bd, "M-wheel", g, [], lambda b, a: bd.composer_text() == "", "wheel over the Board types nothing", act=lambda: bd.lit("\x1b[<64;20;8M"))
    step(bd, "M-right", g, [], lambda b, a: bd.composer_text() == "", "right button types nothing", act=lambda: (bd.lit("\x1b[<2;20;8M"), bd.lit("\x1b[<2;20;8m")))
    bd.home()


def walk_firstuse(bd):
    """The Board of a project with no run: the call to action, the
    provider verify (armed, then cancelled: the probe calls the model),
    the message line and the footer buttons. SKIP on a Board with a run."""
    g = "firstuse"
    bd.home()
    s = bd.cap()
    if "NO RUN" not in (s.splitlines()[0] if s else ""):
        emit("FU-any", g, "SKIP", "a run is open: no first-use card")
        return
    for cid, rx in [("FU-cta", r"Tell the Marshal what to build"), ("FU-steps", r"Give a goal[\s\S]*Accept the architecture[\s\S]*Watch the run"), ("FU-providers", r"PROVIDERS")]:
        emit(cid, g, "PASS" if re.search(rx, s) else "FAIL", "the card shows %s" % rx[:40])
    # v arms the next unverified provider; Esc cancels without verifying.
    if re.search(r"unverified", s):
        step(bd, "FU-v", g, ["v"], has(r"Enter verifies \w+"), "v arms the verify of the next unverified provider")
        step(bd, "FU-v-esc", g, ["Escape"], has(r"cancelled"), "Esc cancels the armed verify")
        bd.home()
        ls = bd.cap().splitlines()
        pos = [(y, l.find("unverified")) for y, l in enumerate(ls) if "unverified" in l and "PROVIDERS" not in l]
        if pos:
            y, x = pos[0]
            step(bd, "FU-chip-click", g, [], has(r"Enter verifies \w+"), "a click on an unverified provider arms its verify", act=lambda x=x, y=y: bd.click(x + 1, y + 1))
            bd.keys("Escape")
            time.sleep(0.4)
    else:
        emit("FU-v", g, "SKIP", "no unverified provider")
    bd.home()
    step(bd, "FU-s", g, ["s"], lambda b, a: bd.saying(), "s opens the message line")
    bd.keys("Escape")
    time.sleep(0.3)
    # The call to action and Enter both move focus to the docked chat.
    for cid, how in [("FU-Enter", "key"), ("FU-cta-click", "click")]:
        bd.home()
        before = bd.window_active()
        if how == "key":
            bd.keys("Enter")
        else:
            ls = bd.cap().splitlines()
            y = next((i for i, l in enumerate(ls) if "Tell the Marshal what to build" in l), -1)
            if y < 0:
                emit(cid, g, "FAIL", "call to action not on screen")
                continue
            bd.click(ls[y].find("Tell") + 1, y + 1)
        time.sleep(1)
        now = bd.window_active()
        emit(cid, g, "PASS" if now != bd.pane else ("DEAD" if now == before else "FAIL"), "the call to action (%s) focuses the chat (%s -> %s)" % (how, before, now))
        if now != bd.pane:
            tmux("select-pane", "-t", bd.pane)
            time.sleep(0.3)
    bd.home()


def commands_from(path):
    src = open(path, encoding="utf-8").read()
    m = re.search(r"Commands: \[\]string\{(.*?)\n\t\t\},", src, re.S)
    return re.findall(r'"(/[^"]+)"', m.group(1)) if m else []


def walk_commands(bd, commands):
    g = "commands"
    unknown = re.compile(r"[Uu]nknown command|not a command|no such command")
    for cmd in commands:
        bd.home()
        bd.lit(cmd)
        time.sleep(0.3)
        if cmd in NO_ENTER:
            comp = bd.composer_text()
            st = "PASS" if comp.startswith(cmd) else "FAIL"
            emit("C" + cmd, g, st, "typed only (acts when entered): composer %r" % comp[:80])
            bd.keys("C-u")
            continue
        before = bd.cap()
        bd.keys("Enter")
        time.sleep(1.6)
        after = bd.cap()
        bd.save("C%s.ans" % cmd.replace("/", "_"), bd.cap(True))
        text = after
        if PANIC.search(text):
            emit("C" + cmd, g, "FAIL", "panic: " + PANIC.search(text).group(0))
        elif unknown.search(text):
            emit("C" + cmd, g, "FAIL", "unknown: " + diff_hint(before, after))
        elif re.search(r"invalid input|internal error", text):
            emit("C" + cmd, g, "FAIL", "error: " + diff_hint(before, after))
        elif norm(before) == norm(after):
            emit("C" + cmd, g, "DEAD", "no answer on screen")
        else:
            emit("C" + cmd, g, "PASS", diff_hint(before, after))
        # Close whatever the command opened.
        bd.keys("Escape")
        time.sleep(0.4)


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--target", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--groups", default="firstuse,board,views,inspector,inbox,mouse,commands")
    ap.add_argument("--commands-from", default="internal/tui/workspace.go")
    ap.add_argument("--only-commands", default="")
    a = ap.parse_args(argv)
    os.makedirs(a.out, exist_ok=True)
    bd = Board(a.target, a.out)
    groups = a.groups.split(",")
    if "firstuse" in groups:
        walk_firstuse(bd)
    if "board" in groups:
        walk_board(bd)
    if "views" in groups:
        walk_views(bd)
    if "inspector" in groups:
        walk_inspector(bd)
    if "inbox" in groups:
        walk_inbox(bd)
    if "mouse" in groups:
        walk_mouse(bd)
    if "commands" in groups:
        cmds = [c for c in a.only_commands.split(",") if c] or commands_from(a.commands_from)
        walk_commands(bd, cmds)
    bd.home()
    return 0


if __name__ == "__main__":
    sys.exit(main())
