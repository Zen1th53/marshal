#!/usr/bin/env python3
"""Paint Marshal Board target screens on a character-cell canvas.
Outputs ANSI (truecolor) files and an HTML page that shows exactly the cells."""
import html, sys, os

W = 132
C = dict(
    ink="#e6e9ee", dim="#8b96a8", faint="#5a6577", acc="#6cb8ff", ok="#5fd39a",
    warn="#f0c062", bad="#ff6f7a", vio="#b79cff", rule="#2e3848", bg="#0e1218",
    top="#172131", now="#13263a", youok="#0f2a1d", youw="#2e2410", sel="#152a40",
    modal="#141d2a", dark="#06121f",
)

class Canvas:
    def __init__(s, h):
        s.h = h
        s.cells = [[[" ", C["ink"], C["bg"], False] for _ in range(W)] for _ in range(h)]
    def put(s, x, y, text, fg=None, bg=None, b=False):
        for i, ch in enumerate(text):
            if 0 <= x + i < W and 0 <= y < s.h:
                c = s.cells[y][x + i]
                c[0] = ch
                c[1] = fg or C["ink"]
                if bg: c[2] = bg
                c[3] = b
        return x + len(text)
    def segs(s, x, y, parts):
        for p in parts:
            if isinstance(p, str): p = (p,)
            t = p[0]; st = p[1] if len(p) > 1 else {}
            x = s.put(x, y, t, st.get("fg"), st.get("bg"), st.get("b", False))
        return x
    def fill(s, x, y, w, h, bg):
        for yy in range(y, y + h):
            for xx in range(x, x + w):
                if 0 <= xx < W and 0 <= yy < s.h:
                    s.cells[yy][xx] = [" ", C["ink"], bg, False]
    def box(s, x, y, w, h, title="", right="", color=None, tcolor=None, bg=None):
        col = color or C["rule"]
        if bg: s.fill(x, y, w, h, bg)
        s.put(x, y, "╭" + "─" * (w - 2) + "╮", col, bg)
        for yy in range(y + 1, y + h - 1):
            s.put(x, yy, "│", col, bg); s.put(x + w - 1, yy, "│", col, bg)
        s.put(x, y + h - 1, "╰" + "─" * (w - 2) + "╯", col, bg)
        if title:
            s.put(x + 2, y, " ", col, bg)
            xx = x + 3
            for i, part in enumerate(title.split("·", 1)):
                if i == 0:
                    xx = s.put(xx, y, part.strip().upper(), tcolor or C["ink"], bg, True)
                else:
                    xx = s.put(xx, y, " · " + part.strip(), C["dim"], bg)
            s.put(xx, y, " ", col, bg)
        if right:
            s.put(x + w - 3 - len(right) - 1, y, " " + right + " ", C["faint"], bg)
    def dim(s):
        for row in s.cells:
            for c in row:
                c[1] = "#3a4352"; c[3] = False
                if c[2] != C["bg"]: c[2] = "#11161d"
    def ansi(s):
        out = []
        for row in s.cells:
            line = ""; prev = None
            for ch, fg, bg, b in row:
                st = (fg, bg, b)
                if st != prev:
                    f = tuple(int(fg[i:i+2], 16) for i in (1, 3, 5))
                    g = tuple(int(bg[i:i+2], 16) for i in (1, 3, 5))
                    line += "\x1b[0;%s38;2;%d;%d;%d;48;2;%d;%d;%dm" % ("1;" if b else "", *f, *g)
                    prev = st
                line += ch
            out.append(line + "\x1b[0m")
        return "\n".join(out) + "\n"
    def html(s):
        rows = []
        for row in s.cells:
            line = ""; run = ""; prev = None
            def flush():
                nonlocal line, run
                if run:
                    fg, bg, b = prev
                    style = "color:%s;background:%s%s" % (fg, bg, ";font-weight:700" if b else "")
                    line += '<span style="%s">%s</span>' % (style, html.escape(run))
                run = ""
            for ch, fg, bg, b in row:
                st = (fg, bg, b)
                if st != prev:
                    flush(); prev = st
                run += ch
            flush()
            rows.append(line)
        return "\n".join(rows)

def chip(text, bg, fg=None):
    return (" " + text + " ", {"fg": fg or C["dark"], "bg": bg, "b": True})
D = {"fg": C["dim"]}; F = {"fg": C["faint"]}; A = {"fg": C["acc"]}; O = {"fg": C["ok"]}
Wn = {"fg": C["warn"]}; R = {"fg": C["bad"]}; V = {"fg": C["vio"]}; B = {"b": True}

def topbar(c, y, crumbs, state, statebg, meters, marshal=("claude", "opus-5.5", "high")):
    c.fill(0, y, W, 1, C["top"])
    x = c.segs(1, y, [(" MARSHAL ", {"b": True, "bg": C["top"]}), ("v0.3.5", {"fg": C["faint"], "bg": C["top"]}), ("   ", {"bg": C["top"]})])
    for i, (k, v) in enumerate(crumbs):
        if i: x = c.put(x, y, " / ", C["faint"], C["top"])
        x = c.put(x, y, k + " ", C["dim"], C["top"]); x = c.put(x, y, v, C["ink"], C["top"], True)
    x = c.put(x + 2, y, " " + state + " ", C["dark"], statebg, True)
    m = "  ".join(meters) + " "
    mx = W - len(m) - 1
    c.put(mx, y, m, C["dim"], C["top"])
    prov, model, effort = marshal
    seg = [("✳ " if prov == "claude" else "◆ ", {"fg": C["acc"], "bg": "#1d2a3d"}), (prov + " · " + model, {"fg": C["ink"], "bg": "#1d2a3d", "b": True}),
           (" · " + effort + " ", {"fg": C["dim"], "bg": "#1d2a3d"}), (" m ", {"fg": C["ink"], "bg": "#2a3a52", "b": True})]
    ln = sum(len(t) for t, _ in seg) + 1
    c.segs(mx - ln - 2, y, [(" ", {"bg": "#1d2a3d"})] + seg)

def nowstrip(c, y, marshal, work, you, ok=True):
    c.fill(0, y, W, 1, C["bg"])
    x = c.put(0, y, " NOW ", C["acc"], C["now"], True)
    x = c.put(x, y, " marshal ", C["faint"], C["bg"]); x = c.put(x, y, marshal.ljust(34)[:34], C["ink"], C["bg"])
    x = c.put(x, y, "│", C["rule"]); x = c.put(x, y, " work ", C["faint"]); x = c.put(x, y, work.ljust(30)[:30], C["ink"])
    bg = C["youok"] if ok else C["youw"]; fg = C["ok"] if ok else C["warn"]
    c.fill(x, y, W - x, 1, bg)
    x = c.put(x, y, " from you ", C["faint"], bg); c.put(x, y, you, fg, bg, True)

def keybar(c, y, keys):
    x = 1
    for k, v in keys:
        x = c.put(x, y, " " + k + " ", C["ink"], "#222c3a", True); x = c.put(x + 1, y, v, C["faint"]) + 2

def gauge(pct, color, n=10):
    k = round(pct / 100 * n)
    return [("█" * k, {"fg": color}), ("░" * (n - k), F)]

# ---------------- screen 1: command centre ----------------
def screen_running():
    c = Canvas(31)
    topbar(c, 0, [("project", "marshal-work"), ("run", "review-fixes")], "RUNNING", C["acc"],
           ["8 workers", "inbox 0", "19:31"])
    nowstrip(c, 1, "claude Opus · watching reviews", "8 running · 4 queued", "nothing · next ask: final review")
    L, LW, RX, RW = 0, 85, 86, 46
    c.box(L, 3, LW, 5, "Plan · 12 tasks · 18 requirements", "accepted rev 1 · 18:52", C["acc"], C["acc"])
    x = 2
    for name, kind in [("goal", 1), ("reqs", 1), ("design", 1), ("arch", 1), ("tasks", 1), ("build 5/12", 2), ("review", 0), ("merge", 0)]:
        if x > 2: x = c.put(x, 4, " → ", C["faint"])
        if kind == 1: x = c.segs(x, 4, [("✓ " + name, O)])
        elif kind == 2: x = c.segs(x, 4, [chip(name, C["acc"])])
        else: x = c.segs(x, 4, [(name, F)])
    c.segs(2, 6, [("█" * 14, O), ("█" * 17, A), ("░" * 14, F), ("   done 5 · active 6 · eta 45m", D)])
    c.segs(2, 5, [("throughput ", D), ("▁▂▃▅▆▇▇█▇▆▇█", A), ("  5 merged/h · 0 reworked · tokens 1.9M", D)])

    c.box(L, 8, LW, 12, "Lanes · from worker heartbeats", "limit claude 4 · agy 4 · total 12")
    hdr = "   LANE        MODEL        TASK                          STEP          TIME  SIGNAL"
    for hx, ht in [(5, "LANE"), (17, "MODEL"), (30, "TASK"), (60, "STEP"), (72, "TIME"), (77, "SIGNAL")]: c.put(hx, 9, ht, C["faint"])
    lanes = [
        ("⣾", A, "claude-1", "sonnet-5.5", "T03 sub-view external links", "tests", "14m", "8s", O, True),
        ("⣾", A, "claude-2", "sonnet-5.5", "T05 request panel overlap", "editing", "11m", "4s", O, False),
        ("⣾", A, "claude-3", "sonnet-5.5", "T07 legend keyboard focus", "editing", "6m", "12s", O, False),
        ("◎", V, "claude-4", "sonnet-5.5", "review T02 (by agy-1)", "reviewing", "2m", "3s", O, False),
        ("⣾", A, "agy-1", "gemini-3.8", "T04 codec round-trip test", "tests", "9m", "6s", O, False),
        ("⣾", A, "agy-2", "gemini-3.8", "T06 history paging", "editing", "8m", "9s", O, False),
        ("◎", V, "agy-3", "gemini-3.8", "review T01 (by claude-2)", "reviewing", "3m", "2s", O, False),
        ("⣾", Wn, "agy-4", "gemini-3.8", "T09 dashed-change styles", "editing", "7m", "1m40s", Wn, False),
    ]
    for i, (sp, spc, lane, model, task, step, t, sig, sigc, sel) in enumerate(lanes):
        y = 10 + i
        if sel: c.fill(1, y, LW - 2, 1, C["sel"])
        bg = C["sel"] if sel else None
        c.put(3, y, sp, spc["fg"], bg)
        c.put(5, y, lane.ljust(12), C["ink"], bg, sel)
        c.put(17, y, model.ljust(13), C["dim"], bg)
        c.put(30, y, task.ljust(30)[:30], C["ink"], bg)
        c.put(60, y, step.ljust(11), C["dim"], bg)
        c.put(72, y, t.rjust(4), C["ink"], bg)
        c.put(77, y, sig.rjust(6), sigc["fg"], bg)

    c.box(L, 20, LW, 8, "Tasks · 5 of 12 merged", "⏎ open · r restart")
    c.put(2, 21, "  ID   TITLE                         FILES         WRITER  REVIEW  STATE", C["faint"])
    tasks = [("T01", "stale target refusal message", "review.go", "claude", "agy", "✓ merged", O),
             ("T02", "digest-bound review requests", "codec.go", "agy", "claude", "◎ in review", V),
             ("T03", "sub-view external links", "review.html", "claude", "agy", "› building", A),
             ("T05", "request panel overlap", "review.html", "claude", "agy", "› building", A),
             ("T12", "regression matrix sign-off", "matrix.md", "claude", "agy", "· waits T03", F)]
    for i, (tid, ti, fi, wr, rv, st, sc) in enumerate(tasks):
        y = 22 + i
        c.put(4, y, tid, C["ink"], None, True); c.put(9, y, ti, C["ink"]); c.put(39, y, fi, C["dim"])
        c.put(53, y, wr); c.put(61, y, rv)
        c.put(69, y, st, sc["fg"])

    c.box(RX, 3, RW, 7, "Marshal", "m switch", C["acc"], C["acc"])
    c.segs(RX + 2, 4, [("✳ claude", {"b": True}), ("  opus-5.5 · effort high", {}), ("   ", {}), (" in use ", {"fg": C["ok"], "bg": "#18212d", "b": True})])
    c.segs(RX + 2, 5, [("doing   ", D), ("watching reviews · next: final", {})])
    c.segs(RX + 2, 6, [("context ", D)] + gauge(41, C["acc"]) + [("  41% · 82k of 200k", D)])
    c.segs(RX + 2, 7, [("limit   ", D)] + gauge(58, C["ok"]) + [("  5h 58% · weekly 71%", D)])
    c.segs(RX + 2, 8, [("chat    ", D), ("bound to this run · input clear", O)])

    c.box(RX, 10, RW, 5, "Workers · providers", "v re-check")
    provs = [("✳ claude", 58, C["ok"], "4 lanes", O, "sonnet"), ("◆ agy", 71, C["ok"], "4 lanes", O, "gemini-3.8"),
             ("⬡ codex", 11, C["warn"], "spare", Wn, "wk 11%")]
    for i, (n, p, gc, tag, tc, val) in enumerate(provs):
        y = 11 + i
        c.put(RX + 2, y, n.ljust(10)); c.segs(RX + 12, y, gauge(p, gc))
        c.segs(RX + 24, y, [(" " + tag.ljust(7) + " ", {"fg": tc["fg"], "bg": "#18212d", "b": True}), (" " + val, D)])

    c.box(RX, 15, RW, 4, "Inbox", "i opens")
    c.put(RX + 2, 16, "Nothing waits for you.", C["ok"], None, True)
    c.put(RX + 2, 17, "Next: final review before merge.", C["dim"])

    c.box(RX, 19, RW, 9, "Activity · grouped")
    acts = [("19:30", "merge ", O, "T08 merged after agy review", ""), ("19:28", "review", V, "T02 review on claude-4", ""),
            ("19:22", "signal", Wn, "agy-4 slow heartbeat", "×3"), ("18:52", "plan  ", A, "12 tasks · 8 workers", ""),
            ("18:44", "arch  ", O, "rev 1 accepted in browser", ""), ("18:41", "memory", F, "old chat memory skipped", "×6")]
    for i, (t, k, kc, m, n) in enumerate(acts):
        y = 20 + i
        c.segs(RX + 2, y, [(t, F), ("  " + k, kc), ("  " + m, F if k == "memory" else {})])
        if n: c.put(RX + RW - 2 - len(n), y, n, C["faint"])
    keybar(c, 29, [("s", "say"), ("⏎", "open"), ("a", "architecture"), ("r", "restart task"), ("m", "model"), ("l", "limits"), ("p", "pause all"), ("F11", "chat"), ("space", "actions"), ("?", "help")])
    return c

# ---------------- screen 2: readiness ----------------
def screen_ready():
    c = Canvas(22)
    topbar(c, 0, [("project", "marshal-work")], "READY", C["ok"], ["inbox 0", "18:22"])
    nowstrip(c, 1, "claude Opus · ready", "no run yet", "sign in to agy, then ⏎ say the goal", ok=False)
    c.box(0, 3, 85, 16, "Readiness · 5 of 7 ready", "re-checked 18:22", C["warn"], C["warn"])
    rows = [("✓", O, "Marshal", "claude · Opus 5.5 · signed in", "in use", O),
            ("✓", O, "Workers", "claude Sonnet 5.5 · up to 4 at once", "ready", O),
            ("!", Wn, "Workers", "agy is installed but not signed in", "action", Wn),
            (None, None, "", "run  agy  once in any terminal and sign in · g opens it here", "", None),
            ("!", Wn, "Fallback", "codex signed in · weekly 11% · resets 21 Nov", "low", Wn),
            ("✓", O, "Platform", "Linux amd64 · workers can run here", "ready", O),
            ("✓", O, "Repo", "clean · main @ abb617e · 2 earlier Codex sessions", "ready", O),
            ("✓", O, "Browser", "no display: links are shown on the Board", "ready", O)]
    for i, (m, mc, k, v, tag, tc) in enumerate(rows):
        y = 5 + i
        if m: c.put(3, y, m, mc["fg"], None, True)
        c.put(6, y, k, C["dim"])
        c.put(17, y, v, C["dim"] if m is None else C["ink"])
        if tag: c.put(85 - 3 - len(tag) - 2, y, " " + tag + " ", tc["fg"], "#18212d", True)
    x = c.segs(3, 15, [chip("g  sign in to agy", C["warn"], "#2a1c00")]); x = c.segs(x + 2, 15, [chip("⏎  say what to build", C["acc"])])
    c.segs(x + 2, 15, [(" v  check again ", {"fg": C["dim"], "bg": "#1a2230"})])
    c.box(86, 3, 46, 8, "Usage counts")
    c.put(88, 4, "Anonymous install and version counts"); c.put(88, 5, "go to marshal.blackhat.uz.")
    c.put(88, 6, "No code, prompts or file names.", C["dim"])
    x = c.segs(88, 8, [chip("y keep on", C["acc"])]); x = c.segs(x + 1, 8, [(" t turn off ", {"fg": C["dim"], "bg": "#1a2230"})])
    c.segs(x + 1, 8, [(" ? what is sent ", {"fg": C["dim"], "bg": "#1a2230"})])
    c.box(86, 11, 46, 8, "Defaults · this project")
    for i, (k, v) in enumerate([("workers", "claude 4 · agy 4"), ("fallback", "codex"), ("review", "other provider"), ("approval", "you: architecture, merge")]):
        c.put(88, 12 + i, k.ljust(10), C["dim"]); c.put(98, 12 + i, v)
    keybar(c, 20, [("⏎", "say what to build"), ("g", "sign in to agy"), ("v", "check again"), ("m", "Marshal model"), ("?", "help")])
    return c

# ---------------- screen 3: planning ----------------
def screen_planning():
    c = Canvas(22)
    topbar(c, 0, [("project", "marshal-work")], "PLANNING", C["vio"], ["inbox 0", "18:47"])
    nowstrip(c, 1, "claude Opus · tasks · 6m", "none yet", "nothing · plan in ≈ 5m")
    c.box(0, 3, 85, 3, "Plan", "", C["acc"], C["acc"])
    x = 2
    for name, kind in [("goal", 1), ("reqs", 1), ("design", 1), ("arch rev 1", 1), ("tasks", 2), ("build", 0), ("review", 0), ("merge", 0)]:
        if x > 2: x = c.put(x, 4, " → ", C["faint"])
        if kind == 1: x = c.segs(x, 4, [("✓ " + name, O)])
        elif kind == 2: x = c.segs(x, 4, [chip(name + " · 6m", C["acc"])])
        else: x = c.segs(x, 4, [(name, F)])
    c.box(0, 6, 85, 13, "Marshal steps · from its tool calls", "quiet 0m")
    steps = [("18:47", "draft", "12 tasks · review.html edits ordered in 3 waves"),
             ("18:46", "assign", "4 claude sonnet · 4 agy gemini-3.8 · review on other"),
             ("18:45", "check", "old branch dogfood/review-page-fixes: none · use main"),
             ("18:43", "read", "requirements.md · design.md · 18-row regression matrix"),
             ("18:41", "read", "architecture rev 1 · 14 parts · 37 links · 2 pages"),
             ("18:41", "start", "planning on claude Opus 5.5 (plan model)")]
    for i, (t, k, m) in enumerate(steps):
        y = 7 + i
        c.segs(2, y, [(t, F), ("  " + k.ljust(7), A), ("  " + m, {})])
    c.segs(2, 16, [("after 10 quiet minutes:  ", D), (" f ", {"fg": C["ink"], "bg": "#222c3a", "b": True}), ("  continue this step on a faster model", D)])
    c.box(86, 3, 46, 7, "Requests to the Marshal")
    c.put(88, 4, "SENT   REQUEST                   ANSWER", C["faint"])
    for i, (t, r, a, ac) in enumerate([("18:44", "apply architecture", "allow 3s", O), ("18:42", "parallel limit 8", "allow 5s", O), ("18:41", "memory import", "decline 4s", D)]):
        c.segs(88, 5 + i, [(t, F), ("  " + r.ljust(24), {}), (a, ac)])
    c.box(86, 10, 46, 9, "Chat · Marshal")
    for i, (k, v, vc) in enumerate([("binding", "this run · live", O), ("restarts", "1 · rebound 18:41", {}), ("re-sent", "2 open requests", {}), ("input", "clear", O), ("model", "claude Opus 5.5", {}), ("context", "41% used", D)]):
        c.segs(88, 11 + i, [(k.ljust(10), D), (v, vc)])
    keybar(c, 20, [("F11", "Marshal chat"), ("s", "say"), ("a", "architecture"), ("f", "faster model"), ("space", "actions"), ("?", "help")])
    return c

# ---------------- screen 4: decision overlay ----------------
def screen_decision():
    c = screen_planning()
    c.dim()
    topbar(c, 0, [("project", "marshal-work")], "YOUR TURN", C["warn"], ["inbox 1", "18:42"])
    nowstrip(c, 1, "waiting on you", "none yet", "accept architecture rev 1 · a", ok=False)
    x0, y0, w, h = 14, 3, 104, 17
    c.box(x0, y0, w, h, "Accept architecture · rev 1", "esc later", C["warn"], C["warn"], C["modal"])
    M = C["modal"]
    c.segs(x0 + 2, y0 + 1, [("14 parts · 37 links · covers R1–R18 · ", {"bg": M}), ("5 parts changed by this goal (dashed)", {"fg": C["warn"], "bg": M})])
    def node(x, y, name, sub, chg=False):
        col = C["warn"] if chg else C["rule"]
        ww = 22
        hz, vt = ("┄", "┆") if chg else ("─", "│")
        c.put(x, y, "╭" + hz * (ww - 2) + "╮", col, M)
        c.put(x, y + 1, vt, col, M); c.put(x + 2, y + 1, name, C["ink"], M, True); c.put(x + ww - 1, y + 1, vt, col, M)
        c.put(x, y + 2, vt, col, M); c.put(x + 2, y + 2, sub, C["faint"], M); c.put(x + ww - 1, y + 2, vt, col, M)
        c.put(x, y + 3, "╰" + hz * (ww - 2) + "╯", col, M)
    xs = [x0 + 3, x0 + 28, x0 + 53, x0 + 78]
    node(xs[0], y0 + 3, "Board", "owner UI"); node(xs[1], y0 + 3, "Runtime", "runs · leases"); node(xs[2], y0 + 3, "Workers", "claude · agy"); node(xs[3], y0 + 3, "architecture-review", "18 fixes", True)
    node(xs[0], y0 + 8, "Marshal chat", "MCP tools"); node(xs[1], y0 + 8, "SQLite state", "revisions"); node(xs[2], y0 + 8, "architecturecodec", "digests", True); node(xs[3], y0 + 8, "review tests", "18 regressions", True)
    c.put(xs[0] + 22, y0 + 4, "──▶", C["dim"], M); c.put(xs[1] + 22, y0 + 4, "──▶", C["dim"], M); c.put(xs[2] + 22, y0 + 4, "┄┄▶", C["warn"], M)
    c.put(xs[0] + 22, y0 + 9, "◀─▶", C["dim"], M); c.put(xs[1] + 22, y0 + 9, "◀─▶", C["dim"], M); c.put(xs[2] + 22, y0 + 9, "┄┄▶", C["warn"], M)
    c.put(xs[3] + 10, y0 + 7, "┆", C["warn"], M)
    c.segs(x0 + 2, y0 + 12, [("No display here, so open this on the machine:  ", {"fg": C["dim"], "bg": M}), ("http://127.0.0.1:37667/?token=7964…462b", {"fg": C["acc"], "bg": M})])
    c.segs(x0 + 2, y0 + 13, [("⚠ The Marshal tried to accept this for you at 18:40. Refused: only you can accept.", {"fg": C["warn"], "bg": M})])
    x = c.segs(x0 + 2, y0 + 15, [chip("a  open in browser", C["acc"])])
    for t in ["c  copy link", "⏎  accept here", "k  comment"]:
        x = c.segs(x + 2, y0 + 15, [(" " + t + " ", {"fg": C["ink"], "bg": "#222c3a"})])
    return c

# ---------------- screen 5: paused ----------------
def screen_paused():
    c = Canvas(19)
    topbar(c, 0, [("project", "marshal-work")], "2 PAUSED", C["bad"], ["inbox 1", "20:05"])
    nowstrip(c, 1, "waiting on your choice", "6 running · 2 paused", "press ⏎ to move 2 tasks to claude", ok=False)
    c.box(0, 3, 85, 13, "Paused · agy Gemini 5h limit", "resets 21:10", C["bad"], C["bad"])
    c.put(2, 4, "agy-2 and agy-4 hit the agy Gemini 5-hour limit. Their work is kept.")
    c.put(2, 6, "  TASK                         PROGRESS                    KEPT", C["faint"])
    c.segs(4, 7, [("T06 history paging          ", {}), ("edits done · tests not run  ", D), ("branch + 3 commits", O)])
    c.segs(4, 8, [("T09 dashed-change styles    ", {}), ("half edited                 ", D), ("branch + 1 commit", O)])
    c.segs(2, 10, [("Enter will: ", D), ("move T06 and T09 to claude sonnet in fresh sessions, keep commits.", {})])
    c.segs(2, 11, [("            review for these two moves to codex.", D)])
    x = c.segs(2, 13, [chip("⏎  move to claude", C["acc"])])
    for t in ["w  wait until 21:10", "d  raw error", "x  stop the run"]:
        x = c.segs(x + 2, 13, [(" " + t + " ", {"fg": C["ink"], "bg": "#222c3a"})])
    c.box(86, 3, 46, 8, "Providers")
    for i, (n, p, gc, tag, tc, val) in enumerate([("✳ claude", 52, C["ok"], "ready", O, "52%"), ("◆ agy", 0, C["bad"], "out", R, "21:10"), ("⬡ codex", 10, C["warn"], "low", Wn, "10%")]):
        y = 4 + i
        c.put(88, y, n.ljust(12)); c.segs(100, y, gauge(p, gc) if p else [("░" * 10, R)])
        c.segs(112, y, [(" " + tag.ljust(6) + " ", {"fg": tc["fg"], "bg": "#18212d", "b": True}), ("  " + val, {})])
    c.put(88, 8, "claude has room for both (≈ 6% each).", C["dim"])
    c.box(86, 11, 46, 5, "Run")
    c.segs(88, 12, [("█" * 14, O), ("███", A), ("░" * 7, F), ("  7 of 12 merged", D)])
    c.put(88, 13, "6 lanes running · no other stops", C["dim"])
    keybar(c, 17, [("⏎", "move to claude"), ("w", "wait"), ("d", "details"), ("x", "stop run"), ("space", "actions")])
    return c

# ---------------- screen 6: Marshal chat (F11) ----------------
def marshal_strip(c, y, x0=0, w=W, stage="planning the tasks · 6m"):
    c.fill(x0, y, w, 1, "#101b2a")
    x = c.segs(x0, y, [(" MARSHAL CHAT ", {"fg": C["dark"], "bg": C["acc"], "b": True}), (" ", {"bg": "#101b2a"}),
                       ("✳ claude · opus-5.5 · medium", {"fg": C["ink"], "bg": "#101b2a", "b": True}),
                       ("  │ ", {"fg": C["rule"], "bg": "#101b2a"}), ("bound to run ", {"fg": C["dim"], "bg": "#101b2a"}),
                       ("● live", {"fg": C["ok"], "bg": "#101b2a", "b": True}), ("  │ ", {"fg": C["rule"], "bg": "#101b2a"}),
                       ("stage ", {"fg": C["dim"], "bg": "#101b2a"}), (stage, {"fg": C["vio"], "bg": "#101b2a"})])
    return x

def claude_code(c, x0, y0, w, h, compact=False):
    """The provider CLI area: drawn as the provider draws it; Marshal does not restyle it."""
    PINK = "#e8807e"
    c.put(x0 + 1, y0 + 1, "▐▛███▜▌", PINK); c.put(x0 + 1, y0 + 2, "▝▜█████▛▘", PINK); c.put(x0 + 1, y0 + 3, "  ▘▘ ▝▝", PINK)
    c.segs(x0 + 12, y0 + 1, [("Claude Code", {"b": True}), (" v2.1.329", D)])
    c.put(x0 + 12, y0 + 2, "Opus 5.5 with medium effort · Claude Max", C["dim"])
    c.put(x0 + 12, y0 + 3, "~/marshal-work", C["dim"])
    y = y0 + 5
    msgs = [("●", "Goal recorded (rev 3). Architecture rev 1 is accepted;"),
            (" ", "reading it through the contract tool, page 1 of 2."),
            ("●", "marshal - marshal_project_contract_context (MCP)"),
            (" ", "  ⎿ 14 parts · 37 links · 5 changed by this goal"),
            ("●", "Splitting 18 fixes into 12 tasks for 4 claude sonnet"),
            (" ", "and 4 agy gemini-3.8 workers; review.html in 3 waves.")]
    if compact: msgs = msgs[:4]
    for i, (b, t) in enumerate(msgs):
        c.put(x0 + 1, y + i, b, C["ink"]); c.put(x0 + 3, y + i, t[: w - 4], C["ink"] if b != " " else C["dim"])
    yy = y + len(msgs) + 1
    c.segs(x0 + 1, yy, [("✻ ", {"fg": PINK}), ("Planning… (2m 10s · ↓ 4.1k tokens)", D)])
    c.put(x0 + 1, y0 + h - 4, "─" * (w - 2), C["rule"])
    c.segs(x0 + 1, y0 + h - 3, [("❯ ", {}), ("│", A)])
    c.put(x0 + 1, y0 + h - 2, "─" * (w - 2), C["rule"])
    c.put(x0 + 1, y0 + h - 1, "⏵⏵ don't ask on (shift+tab to cycle)", C["faint"])

def screen_chat():
    c = Canvas(26)
    topbar(c, 0, [("project", "marshal-work")], "PLANNING", C["vio"], ["inbox 0", "18:47"], ("claude", "opus-5.5", "medium"))
    marshal_strip(c, 1, stage="tasks · 6m")
    c.put(W - 35, 1, "3 requests answered · input clear ", C["dim"], "#101b2a")
    c.box(0, 2, W, 21, "Claude Code · Marshal's own session", "the provider draws this area", C["rule"])
    claude_code(c, 1, 3, W - 2, 19)
    keybar(c, 24, [("F11", "Board"), ("Alt+←", "split with Board"), ("m", "change Marshal model"), ("s", "say from Board"), ("i", "inbox"), ("?", "help")])
    return c

# ---------------- screen 7: split: chat left, Board right ----------------
def screen_split():
    c = Canvas(30)
    topbar(c, 0, [("project", "marshal-work"), ("run", "review-fixes")], "RUNNING", C["acc"], ["inbox 0", "19:31"], ("claude", "opus-5.5", "medium"))
    LW = 66
    c.fill(0, 1, LW, 1, "#101b2a")
    c.segs(0, 1, [(" MARSHAL CHAT ", {"fg": C["dark"], "bg": C["acc"], "b": True}), (" ✳ opus-5.5 · medium  ", {"fg": C["ink"], "bg": "#101b2a", "b": True}), ("● live", {"fg": C["ok"], "bg": "#101b2a", "b": True}), ("  input clear", {"fg": C["dim"], "bg": "#101b2a"})])
    c.box(0, 2, LW, 25, "Claude Code", "", C["rule"])
    claude_code(c, 1, 3, LW - 2, 23, compact=False)
    RX = LW + 1; RW = W - RX
    c.fill(RX, 1, RW, 1, C["bg"])
    c.segs(RX, 1, [(" NOW ", {"fg": C["acc"], "bg": C["now"], "b": True}), (" 8 running · 5/12 merged ", {}), (" from you nothing ", {"fg": C["ok"], "bg": C["youok"], "b": True})])
    c.box(RX, 2, RW, 4, "Plan", "", C["acc"], C["acc"])
    c.segs(RX + 2, 3, [("✓ arch → ✓ tasks → ", O), (" build 5/12 ", {"fg": C["dark"], "bg": C["acc"], "b": True}), (" → review → merge", F)])
    c.segs(RX + 2, 4, [("█" * 10, O), ("█" * 12, A), ("░" * 10, F), ("  eta 45m", D)])
    c.box(RX, 6, RW, 11, "Lanes · heartbeats", "")
    lanes = [("⣾", A, "claude-1", "T03 sub-view links", "8s"), ("⣾", A, "claude-2", "T05 request panel", "4s"), ("⣾", A, "claude-3", "T07 legend focus", "12s"),
             ("◎", V, "claude-4", "review T02", "3s"), ("⣾", A, "agy-1", "T04 codec test", "6s"), ("⣾", A, "agy-2", "T06 history paging", "9s"),
             ("◎", V, "agy-3", "review T01", "2s"), ("⣾", Wn, "agy-4", "T09 dashed styles", "1m40s")]
    for i, (sp, sc, ln, t, sig) in enumerate(lanes):
        y = 7 + i
        c.put(RX + 2, y, sp, sc["fg"]); c.put(RX + 4, y, ln.ljust(10)); c.put(RX + 14, y, t.ljust(28)[:28])
        c.put(RX + RW - 2 - len(sig), y, sig, C["warn"] if "m" in sig else C["ok"])
    c.box(RX, 17, RW, 5, "Workers · providers", "")
    for i, (n, p, gc, v) in enumerate([("✳ claude", 58, C["ok"], "4 lanes · sonnet"), ("◆ agy", 71, C["ok"], "4 lanes · gemini-3.8"), ("⬡ codex", 11, C["warn"], "spare · wk 11%")]):
        c.put(RX + 2, 18 + i, n.ljust(10)); c.segs(RX + 12, 18 + i, gauge(p, gc)); c.put(RX + 24, 18 + i, v, C["dim"])
    c.box(RX, 22, RW, 5, "Activity", "")
    for i, (t, k, kc, m) in enumerate([("19:30", "merge ", O, "T08 merged after agy review"), ("19:28", "review", V, "T02 review on claude-4"), ("19:22", "signal", Wn, "agy-4 slow heartbeat ×3")]):
        c.segs(RX + 2, 23 + i, [(t, F), ("  " + k, kc), ("  " + m, {})])
    keybar(c, 28, [("Alt+←", "chat"), ("Alt+→", "Board"), ("F11", "full chat"), ("m", "Marshal model"), ("space", "actions")])
    return c

SCREENS = [
    ("Command centre, run in progress", "P1 lanes · P2 NOW · P6 limits · U3 grouped activity · U5 in use", screen_running),
    ("First open: readiness check", "P5 platform · U4 agy reason · U5 in use · U6 links · P12 telemetry ask", screen_ready),
    ("Planning: the Marshal's work is visible", "P3 steps · P4 plan model · U1 delivery · U2 rebind", screen_planning),
    ("Your turn: accept the architecture", "U6 link shown · P8 self-approval · P11 dashed changes", screen_decision),
    ("Run paused: one key to recover", "P7 recovery · P6 move work", screen_paused),
    ("Marshal chat (F11): the Marshal's own session", "Marshal model · effort · binding · stage · requests", screen_chat),
    ("Split: Marshal chat beside the Board (Alt+←)", "chat left · compact Board right", screen_split),
]

if __name__ == "__main__":
    outdir = sys.argv[1]
    blocks = []
    for i, (title, tags, fn) in enumerate(SCREENS, 1):
        c = fn()
        open(os.path.join(outdir, "screen%d.ans" % i), "w").write(c.ansi())
        blocks.append((title, tags, c.html(), c.h))
    page = open(os.path.join(outdir, "marshal-tui-real.tpl.html")).read()
    body = ""
    for title, tags, pre, h in blocks:
        body += '<section><div class="cap"><h2>%s</h2><span class="tags">%s</span></div><div class="term"><div class="bar"><i></i><i></i><i></i><span>132 × %d · truecolor terminal</span></div><div class="scroll"><pre>%s</pre></div></div></section>\n' % (html.escape(title), html.escape(tags), h, pre)
    open(os.path.join(outdir, "marshal-tui-real.html"), "w").write(page.replace("<!--SCREENS-->", body))
    print("ok")
