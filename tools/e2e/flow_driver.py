#!/usr/bin/env python3
"""Drive a Marshal chat end to end as the owner of a scratch project.

Answers the Marshal one question at a time (language, goal, architecture
acceptance, plan approval, close), watches every marshal-* window
for popups / native permission prompts, polls canonical task state through
MCP stdio, and records how long each phase took.

Prints one JSON object per check on stdout:
  {"id": "...", "status": "PASS|FAIL|INFO", "seconds": n, "evidence": "..."}
and writes the transcript to OUT/flow-transcript.txt.

Usage: flow_driver.py --session S --project DIR --bin MARSHAL --goal TEXT
                      --captures DIR --out DIR [--provider codex] [--timeout 2400]
Requires TMUX_TMPDIR to point at the isolated tmux server.
"""
import argparse
import hashlib
import json
import os
import re
import signal
import subprocess
import sys
import time

BUSY = re.compile(r"esc to interrupt|Working \(|Thinking|· esc|tokens ·|\bRunning\b.*esc", re.I)
TRUST = re.compile(r"Trust this folder|Do you trust|trust the files in this folder", re.I)
# Provider-native tool approval dialogs (codex: "Allow the marshal MCP server
# to run tool ...?"; claude: "Do you want to proceed?").
# Claude's dialog is a line of its own; a Marshal sentence such as "How do
# you want to proceed?" is not a dialog.
TOOL_PROMPT = re.compile(r"Allow the \S+ MCP server to run tool|enter to submit \| esc to cancel|^\s*Do you want to proceed\?|Allow for this session|Would you like to run the following command|Press enter to confirm or esc to cancel", re.I | re.M)


def tmux(*a):
    return subprocess.run(["tmux", *a], capture_output=True, text=True).stdout


def cap(t, history=0):
    args = ["capture-pane", "-p", "-t", t]
    if history:
        args += ["-S", "-%d" % history]
    return tmux(*args)


def emit(cid, status, seconds, evidence):
    print(json.dumps({"id": cid, "status": status, "seconds": round(seconds, 1), "evidence": evidence[:400]}), flush=True)


class Flow:
    def __init__(self, a):
        self.a = a
        self.t0 = time.time()
        self.phase = {}
        self.popups = []
        self.replies = []
        self.transcript = open(os.path.join(a.out, "flow-transcript.txt"), "w")
        self.popup_re = re.compile(a.popup_re, re.I)
        self.capn = 60

    def log(self, msg):
        line = "%7.1fs %s" % (time.time() - self.t0, msg)
        self.transcript.write(line + "\n")
        self.transcript.flush()

    def mark(self, name):
        if name not in self.phase:
            self.phase[name] = time.time() - self.t0
            self.log("PHASE " + name)
            self.save("phase-" + name)

    def save(self, name):
        self.capn += 1
        for w in self.windows():
            safe = re.sub(r"[^A-Za-z0-9_-]", "-", w.split(":", 1)[1])
            with open(os.path.join(self.a.captures, "%02d-%s-%s.txt" % (self.capn, name, safe)), "w") as fh:
                fh.write(cap(w))

    def windows(self):
        out = tmux("list-windows", "-t", self.a.session, "-F", "#{session_name}:#{window_index}:#{window_name}:#{pane_current_command}")
        return [":".join(l.split(":")[:2]) + ":" + l.split(":")[2] for l in out.splitlines() if l]

    def target(self, w):
        return ":".join(w.split(":")[:2])

    def chat(self):
        for w in self.windows():
            if "-chat-" in w:
                return self.target(w)
        return None

    def scan_popups(self):
        found = []
        ps = subprocess.run(["pgrep", "-af", "display-popup"], capture_output=True, text=True).stdout
        for l in ps.splitlines():
            if "pgrep" not in l:
                found.append("tmux display-popup: " + l[:160])
        for w in self.windows():
            for line in cap(self.target(w)).splitlines():
                if self.popup_re.search(line):
                    found.append("%s: %s" % (w.split(":", 2)[2], line.strip()[:160]))
                    break
        for f in found:
            if f not in self.popups:
                self.popups.append(f)
                self.log("POPUP " + f)
        return found

    def tasks(self):
        try:
            out = subprocess.run([sys.executable, os.path.join(os.path.dirname(__file__), "mcp_probe.py"),
                                  self.a.bin, self.a.project, "tasks_list", "{}"],
                                 capture_output=True, text=True, timeout=90).stdout
        except subprocess.TimeoutExpired:
            return None
        for l in out.splitlines():
            r = json.loads(l)
            if r.get("step") == "call tasks_list" and r.get("ok"):
                try:
                    data = json.loads(r["result"])
                except ValueError:
                    return r["result"]
                return data
        return None

    def send(self, w, text):
        tmux("send-keys", "-t", w, "-l", text)
        time.sleep(0.5)
        tmux("send-keys", "-t", w, "Enter")
        self.replies.append(text)
        self.log("OWNER> " + text)

    def last_marshal_text(self, screen):
        lines = [l for l in screen.splitlines() if l.strip()]
        # Claude Code draws its input box (── / ❯ / ──) and status lines
        # below the conversation; read only what is above that box.
        for i in range(len(lines) - 1, 0, -1):
            if lines[i].lstrip().startswith("❯") and set(lines[i - 1].strip()) == {"─"}:
                lines = lines[:i - 1]
                break
        # text after our last message
        idx = 0
        for i, l in enumerate(lines):
            if self.replies and self.replies[-1][:40] in l:
                idx = i
        tail = lines[idx + 1:]
        tail = [l for l in tail if not l.lstrip().startswith(("›", ">", "?", "GPT", "gpt"))]
        return "\n".join(tail[-12:])

    def decide(self, text):
        low = text.lower()
        if "language" in low and "language" not in self.asked:
            self.asked.add("language")
            return "English"
        if re.search(r"accept.{0,40}architecture|architecture.{0,60}(accept|approv|confirm)", low):
            self.mark("architecture_asked")
            return "accept architecture"
        if re.search(r"approve.{0,30}plan|plan.{0,40}(approv|ready)|/marshal approve", low):
            self.mark("plan_asked")
            return "approve the plan"
        if re.search(r"close|deliver|merge", low) and self.phase.get("plan_asked"):
            return "Yes, close and deliver the verified result to main."
        # The goal goes out at the first sign the Marshal wants it: a goal-like
        # question, a message without a question after the language answer
        # (it is waiting for the owner), or any question once the language
        # and one other question were answered. A codex Marshal asked
        # "Ready to put MARSHAL to work?" and never got the goal.
        goal_like = re.search(r"goal|what (do|would|should) you|want to (build|achieve|do|work)|objective|describe|work on|help (you )?with|like to do|task", low)
        waiting = "language" in self.asked and "?" not in text
        if "goal" not in self.asked and (goal_like or waiting or len(self.replies) >= 2):
            self.asked.add("goal")
            self.mark("goal_sent")
            return self.a.goal
        if "?" in text:
            return "Use your recommendation."
        return None

    def run(self):
        a = self.a
        self.asked = set()
        deadline = self.t0 + a.timeout
        chat = self.chat()
        if not chat:
            w0 = "%s:0" % a.session
            tmux("send-keys", "-t", w0, "-l", "/marshal chat")
            tmux("send-keys", "-t", w0, "Enter")
            for _ in range(30):
                time.sleep(2)
                chat = self.chat()
                if chat:
                    break
        if not chat:
            emit("D01-marshal-chat-open", "FAIL", time.time() - self.t0, "no chat window")
            return
        # Use the requested Marshal provider; the auto-started chat may differ.
        current = tmux("display", "-p", "-t", chat, "#{pane_current_command}").strip()
        if a.provider and a.provider not in current:
            self.log("chat runs %s; switching with /marshal model %s" % (current, a.provider))
            out = self.tui_command("/marshal model %s" % a.provider, 15)
            emit("D00-marshal-provider-switch", "INFO", time.time() - self.t0,
                 "auto-started chat ran %s; /marshal model %s: %s" % (current, a.provider,
                 " ".join(l.strip() for l in out.splitlines() if "Marshal" in l)[-200:]))
            for _ in range(30):
                time.sleep(2)
                chat = self.chat()
                if chat and a.provider in tmux("display", "-p", "-t", chat, "#{pane_current_command}"):
                    break
        emit("D01-marshal-chat-open", "PASS", time.time() - self.t0,
             "%s %s" % (chat, tmux("display", "-p", "-t", chat, "#{pane_current_command}").strip()))
        self.mark("chat_open")
        last_hash, stable_since, last_task_poll, idle_nudges = "", time.time(), 0, 0
        merged_ref = self.main_ref()
        stop = {"now": False}
        signal.signal(signal.SIGTERM, lambda *_: stop.update(now=True))
        while time.time() < deadline and not stop["now"]:
            time.sleep(3)
            self.scan_popups()
            if not self.chat():
                # The chat window closed (e.g. a native prompt answered "Quit").
                self.tui_command("/marshal chat", 15)
                self.intervene("chat window disappeared; reopened with /marshal chat")
                chat = self.chat()
                if not chat:
                    self.log("chat could not be reopened")
                    break
                continue
            scr = cap(chat, 200)
            if TRUST.search(cap(chat)):
                key = "native trust prompt: Trust this folder?"
                if key not in self.popups:
                    self.popups.append(key)
                if "Yes, I trust this folder" in cap(chat):
                    # Claude Code: "❯ No, exit" is preselected; move to Yes.
                    self.log("POPUP native trust prompt in chat (claude); owner selects 'Yes, I trust this folder'")
                    tmux("send-keys", "-t", chat, "Down")
                    time.sleep(0.4)
                    tmux("send-keys", "-t", chat, "Enter")
                    time.sleep(3)
                    continue
                self.log("POPUP native trust prompt in chat; owner selects 1. Trust and continue")
                tmux("send-keys", "-t", chat, "1")
                time.sleep(0.5)
                if TRUST.search(cap(chat)):
                    tmux("send-keys", "-t", chat, "Enter")
                time.sleep(3)
                continue
            tail15 = "\n".join([l for l in cap(chat).splitlines() if l.strip()][-15:])
            if TOOL_PROMPT.search(tail15):
                what = next((l.strip() for l in scr.splitlines()[-15:] if "Allow the" in l or "Would you like" in l or "$ " in l), "tool approval")
                key = "tool-approval: " + what[:120]
                if key not in self.popups:
                    self.popups.append(key)
                self.tool_prompts = getattr(self, "tool_prompts", 0) + 1
                self.log("POPUP native tool approval (#%d): %s; owner picks 'Allow for this session'" % (self.tool_prompts, what[:160]))
                tmux("send-keys", "-t", chat, "Down")
                time.sleep(0.4)
                tmux("send-keys", "-t", chat, "Enter")
                time.sleep(2)
                continue
            h = hashlib.md5(scr.encode()).hexdigest()
            if h != last_hash:
                last_hash, stable_since = h, time.time()
            busy = bool(BUSY.search("\n".join([l for l in scr.splitlines() if l.strip()][-8:])))
            if time.time() - last_task_poll > 30:
                last_task_poll = time.time()
                self.observe_tasks()
                self.observe_windows()
                self.owner_interventions(scr)
                if self.main_ref() != merged_ref and (self.phase.get("plan_approved") or self.phase.get("tasks_created")):
                    self.mark("closed_to_main")
                    break
            if busy or time.time() - stable_since < 8:
                continue
            if re.search(r"^\s*› ?1\. ", "\n".join([l for l in cap(chat).splitlines() if l.strip()][-20:]), re.M):
                self.log("unrecognised choice menu in chat; not typing into it")
                self.save("choice-menu")
                tmux("send-keys", "-t", chat, "Escape")
                continue
            text = self.last_marshal_text(scr)
            if "language" not in self.asked and not self.replies:
                self.mark("first_question")
            reply = self.decide(text)
            if reply == "approve the plan":
                self.mark("plan_approved")
            if reply is None:
                idle_nudges += 1
                if idle_nudges % 20 == 0:  # ~1 min idle with no question
                    if self.phase.get("plan_approved"):
                        reply = "What is the status? If all tasks are verified, close and deliver to main."
                    else:
                        reply = "Use your recommendation and continue."
            if reply and len(self.replies) >= 2 and self.replies[-1] == reply == self.replies[-2]:
                reply = "That did not work twice. Use your recommendation, do whatever MARSHAL needs next, and tell me exactly which action you need from me."
            if reply:
                self.log("MARSHAL< " + text.replace("\n", " ⏎ ")[-600:])
                self.send(chat, reply)
                stable_since = time.time()
        self.save("end")
        self.finish()

    def w0(self):
        return "%s:0" % self.a.session

    def tui_command(self, text, wait=20):
        w = self.w0()
        for _ in range(6):  # leave navigation; Esc on the workspace opens it
            s = cap(w)
            if re.search(r"Enter go · Esc close|Esc or Enter to dismiss|Type to edit|Proceed", s):
                tmux("send-keys", "-t", w, "Escape")
            elif "System" in (s.splitlines() or [""])[0]:
                tmux("send-keys", "-t", w, "q")
            else:
                break
            time.sleep(0.5)
        tmux("send-keys", "-t", w, "-l", text)
        tmux("send-keys", "-t", w, "Enter")
        time.sleep(wait)
        return cap(w)

    def intervene(self, what):
        self.interventions = getattr(self, "interventions", [])
        self.interventions.append(what)
        self.log("OWNER-INTERVENTION " + what)

    def owner_interventions(self, chat_screen):
        """Owner steps the Marshal may need on older bases; each is recorded."""
        tail = "\n".join(chat_screen.splitlines()[-40:]).lower()
        done = getattr(self, "done_steps", set())
        self.done_steps = done
        # Goal contract missing: create it and confirm it in Control > Approvals.
        # The Marshal formalizes and confirms the Goal itself, so this owner
        # step is taken only when the Marshal still asks the owner for it
        # well after the goal was sent.
        goal_age = time.time() - self.t0 - self.phase.get("goal_sent", 1e9)
        if "goal" not in done and goal_age > 300 and re.search(r"/goal create|/goal <|goal confirmation|control > approvals", tail):
            done.add("goal")
            out = self.tui_command("/goal create " + self.a.goal_short, 15)
            gid = (re.findall(r"GOAL-[0-9a-f-]{8,}", out) or ["?"])[-1]
            self.intervene("/goal create (%s)" % gid)
            w = self.w0()
            tmux("send-keys", "-t", w, "C-n"); time.sleep(1)
            tmux("send-keys", "-t", w, "/"); time.sleep(0.4)
            tmux("send-keys", "-t", w, "-l", "control approvals approve"); time.sleep(0.8)
            for k in ("Enter", "Enter"):
                tmux("send-keys", "-t", w, k); time.sleep(1.2)
            if "Proceed" in cap(w):
                tmux("send-keys", "-t", w, "Right"); time.sleep(0.3)
                tmux("send-keys", "-t", w, "Enter"); time.sleep(3)
            res = [l for l in cap(w).splitlines() if l.startswith("Result ")]
            self.intervene("Control > Approvals > Approve GOAL_CONFIRM: %s" % (res[0].strip() if res else "no result"))
            self.tui_command("", 1)
            self.send(self.chat(), "I created and confirmed the goal in MARSHAL. Please continue.")
        # A published draft that nobody imports: re-arm the importer.
        m = os.path.join(self.a.project, ".marshal", "marshal")
        ready = os.path.join(m, "plan-draft.json.ready")
        if os.path.exists(ready) and os.path.exists(os.path.join(m, "plan-draft.json")):
            age = time.time() - os.path.getmtime(ready)
            if age > 60 and getattr(self, "last_kick", 0) < os.path.getmtime(ready):
                self.last_kick = time.time()
                out = self.tui_command("/marshal chat", 20)
                err = [l.strip() for l in out.splitlines() if "draft rejected" in l or "Error:" in l]
                self.intervene("/marshal chat to import a draft unconsumed for %ds%s" % (age, ("; " + err[-1][:200]) if err else ""))
                if err:
                    self.send(self.chat(), "MARSHAL rejected the draft: %s. Please fix it and republish." % err[-1][:300])

    def main_ref(self):
        ref = os.path.join(self.a.project, ".git", "refs", "heads", "main")
        try:
            return open(ref).read().strip()
        except OSError:
            packed = os.path.join(self.a.project, ".git", "packed-refs")
            try:
                for l in open(packed):
                    if l.strip().endswith("refs/heads/main"):
                        return l.split()[0]
            except OSError:
                pass
        return ""

    def observe_tasks(self):
        data = self.tasks()
        if data is None:
            return
        items = data if isinstance(data, list) else (data.get("tasks") if isinstance(data, dict) else None) or []
        if not isinstance(items, list):
            return
        self.task_snapshot = items
        if items:
            self.mark("tasks_created")
        states = sorted({str(t.get("status")) for t in items if isinstance(t, dict)})
        self.log("TASKS %d %s" % (len(items), states))
        if any(s in ("running", "claimed", "in_progress", "leased") for s in states):
            self.mark("tasks_dispatched")
        if any(s in ("review", "in_review", "verifying") for s in states):
            self.mark("review_started")
        if items and all(s in ("merged", "verified", "done", "accepted") for s in states):
            self.mark("tasks_verified")

    def observe_windows(self):
        provs = set()
        for w in self.windows():
            name = w.split(":", 2)[2]
            if "-task-" in name:
                self.mark("worker_window")
                cmd = tmux("display", "-p", "-t", self.target(w), "#{pane_current_command}").strip()
                provs.add(cmd)
        if provs:
            self.worker_cmds = getattr(self, "worker_cmds", set()) | provs

    def finish(self):
        a = self.a
        total = time.time() - self.t0
        order = ["chat_open", "first_question", "goal_sent", "architecture_asked", "plan_asked", "plan_approved",
                 "tasks_created", "tasks_dispatched", "worker_window", "review_started", "tasks_verified", "closed_to_main"]
        prev = 0.0
        for p in order:
            if p in self.phase:
                emit("D10-phase-" + p, "INFO", self.phase[p] - prev, "t+%.0fs" % self.phase[p])
                prev = self.phase[p]
            else:
                emit("D10-phase-" + p, "FAIL", 0, "phase not reached in %.0fs" % total)
        iv = getattr(self, "interventions", [])
        emit("D19-no-owner-interventions", "PASS" if not iv else "FAIL", 0, "none" if not iv else " | ".join(iv)[:400])
        emit("D20-no-popups-during-flow", "PASS" if not self.popups else "FAIL", total,
             "none" if not self.popups else " | ".join(self.popups[:6]))
        workers = sorted(getattr(self, "worker_cmds", set()))
        emit("D21-two-worker-providers", "PASS" if len(workers) >= 2 else "FAIL", 0, "worker pane commands: %s" % workers)
        # Results in the project (checked from files; close must land on main).
        def read(p):
            try:
                return open(os.path.join(a.project, p)).read()
            except OSError:
                return ""
        ok_test = subprocess.run([sys.executable, "-m", "unittest", "-q"], cwd=a.project, capture_output=True, text=True).returncode == 0
        emit("D30-closed-to-main", "PASS" if "closed_to_main" in self.phase else "FAIL", 0, "main ref %s" % self.main_ref()[:12])
        emit("D31-failing-test-fixed", "PASS" if ok_test else "FAIL", 0, "python3 -m unittest in project working tree")
        emit("D32-readme-line", "PASS" if "Usage" in read("README.md") else "FAIL", 0, read("README.md")[-120:])
        eg = read("egress.txt").strip()
        emit("E01-worker-egress-example-com", "PASS" if eg.startswith(("2", "3")) else "FAIL", 0,
             "egress.txt=%r" % eg[:120] if eg else "egress.txt missing (see transcript/worker captures for the refusal)")
        out = read("outside.txt").strip()
        emit("E02-outside-read-without-grant", "PASS" if "outside-read-marker" in out else "FAIL", 0,
             "outside.txt=%r" % out[:120] if out else "outside.txt missing")
        # Memory: recall from a fresh process after close.
        mem = subprocess.run([a.bin, "memory", "list"], cwd=a.project, capture_output=True, text=True)
        emit("D40-memory-after-close", "INFO", 0, mem.stdout.strip().replace("\n", " ; ")[:400])
        for q in ("calc", "unittest", "README usage"):
            r = subprocess.run([a.bin, "memory", "recall", q], cwd=a.project, capture_output=True, text=True)
            first = r.stdout.splitlines()[0] if r.stdout else r.stderr.strip()
            emit("D41-memory-recall-%s" % q.replace(" ", "-"), "PASS" if re.search(r"results=[1-9]", r.stdout) else "FAIL", 0, first)
        with open(os.path.join(a.out, "popups-flow.txt"), "w") as fh:
            fh.write("\n".join(self.popups))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--session", required=True)
    ap.add_argument("--project", required=True)
    ap.add_argument("--bin", required=True)
    ap.add_argument("--goal", required=True)
    ap.add_argument("--goal-short", default="Fix the failing unit test in test_calc.py and add one usage line to README.md")
    ap.add_argument("--captures", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--provider", default="codex")
    ap.add_argument("--timeout", type=int, default=2400)
    ap.add_argument("--popup-re", default=r"Trust this folder|Do you trust|Allow once|Allow always|\[y/N\]|\(y/n\)")
    a = ap.parse_args()
    os.makedirs(a.captures, exist_ok=True)
    os.makedirs(a.out, exist_ok=True)
    Flow(a).run()
    return 0


if __name__ == "__main__":
    sys.exit(main())
