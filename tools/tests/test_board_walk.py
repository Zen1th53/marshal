import importlib.util
import io
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import textwrap
import time
import unittest
from contextlib import redirect_stdout


ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE_PATH = ROOT / "tools" / "e2e" / "board_walk.py"
GOLDENS = ROOT / "internal" / "tui" / "testdata" / "board"
ANSI = re.compile(r"\x1b\[[0-9;]*m")


def load_walk():
    spec = importlib.util.spec_from_file_location("board_walk", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def golden(name):
    """A Board golden as capture-pane shows it: no colour, trailing blanks cut."""
    text = ANSI.sub("", (GOLDENS / name).read_text(encoding="utf-8"))
    return "\n".join(l.rstrip() for l in text.split("\n"))


class ScreenTests(unittest.TestCase):
    """The walk's screen checks, against the reviewed Board goldens."""

    def setUp(self):
        self.walk = load_walk()

    def test_board_frames_are_recognised_at_every_size(self):
        for name in ["demo-home-100x30.golden", "demo-home-120x40.golden", "demo-home-170x45.golden",
                     "demo-home-200x60.golden", "demo-split-120x40.golden", "demo-home-80x24.golden"]:
            screen = golden(name)
            self.assertTrue(self.walk.board_shown(screen), name)
            self.assertTrue(self.walk.footer_ok(screen), name)
            self.assertEqual(self.walk.focused(screen), "tasks", name)
            self.assertFalse(self.walk.help_open(screen), name)
            self.assertFalse(self.walk.inspector_open(screen), name)
            w = int(re.search(r"-(\d+)x\d+", name).group(1))
            self.assertLessEqual(self.walk.widest(screen), w, name)

    def test_overlays(self):
        self.assertTrue(self.walk.help_open(golden("help-split-120x40.golden")))
        self.assertTrue(self.walk.inspector_open(golden("demo-inspector-120x40.golden")))
        self.assertTrue(self.walk.inspector_open(golden("demo-inspector-200x60.golden")))

    def test_focus_moves(self):
        screen = golden("demo-home-120x40.golden")
        lanes = screen.replace("> TASKS", "TASKS").replace("LANES", "> LANES", 1)
        self.assertEqual(self.walk.focused(lanes), "lanes")
        inbox = screen.replace("> TASKS", "TASKS").replace("INBOX", "> INBOX", 1)
        self.assertEqual(self.walk.focused(inbox), "inbox")

    def test_not_a_board(self):
        for screen in ["", "MARSHAL · Team\nNo Marshal run is open\n", "codex> hello\n"]:
            self.assertFalse(self.walk.board_shown(screen), screen)


FAKE_BOARD = textwrap.dedent('''\
    import os, sys, termios, tty
    frames = {k: open(v, encoding="utf-8").read() for k, v in (a.split("=", 1) for a in sys.argv[1:])}
    def show(name, focus="tasks"):
        f = frames[name]
        if focus != "tasks":
            f = f.replace("> TASKS", "TASKS").replace({"lanes": "LANES", "inbox": "INBOX"}[focus], "> " + {"lanes": "LANES", "inbox": "INBOX"}[focus], 1)
        sys.stdout.write("\\x1b[H\\x1b[2J" + f.rstrip("\\n").replace("\\n", "\\r\\n"))
        sys.stdout.flush()
    fd = sys.stdin.fileno()
    tty.setraw(fd)
    show("home")
    while True:
        k = os.read(fd, 8).decode()
        if k == "?":
            show("help")
        elif k == "\\r":
            show("inspector")
        elif k in ("\\x1b", "t", "j"):
            show("home")
        elif k == "l":
            show("home", "lanes")
        elif k == "i":
            show("home", "inbox")
''')


@unittest.skipUnless(shutil.which("tmux"), "tmux not installed")
class WalkTests(unittest.TestCase):
    """The walk against a pane on a private tmux server that answers the
    Board's keys with the goldens."""

    def setUp(self):
        self.walk = load_walk()
        self.dir = tempfile.TemporaryDirectory(prefix="bw-")
        self.addCleanup(self.dir.cleanup)
        self.tmux_dir = tempfile.TemporaryDirectory(dir="/tmp", prefix="tmx-")
        self.addCleanup(self.tmux_dir.cleanup)
        d = pathlib.Path(self.dir.name)
        self.env = dict(os.environ, TMUX_TMPDIR=self.tmux_dir.name)
        self.env.pop("TMUX", None)
        self.env.pop("TMUX_PANE", None)
        frames = []
        for key, name in [("home", "demo-home-170x45.golden"), ("help", "help-split-120x40.golden"),
                          ("inspector", "demo-inspector-120x40.golden")]:
            path = d / (key + ".txt")
            path.write_text(golden(name), encoding="utf-8")
            frames.append("%s=%s" % (key, path))
        (d / "fake.py").write_text(FAKE_BOARD, encoding="utf-8")
        self.tmux("new-session", "-d", "-s", "bw", "-x", "220", "-y", "60",
                  sys.executable, "-I", str(d / "fake.py"), *frames)
        self.addCleanup(lambda: self.tmux("kill-server"))
        old = dict(os.environ)
        os.environ.clear()
        os.environ.update(self.env)
        self.addCleanup(lambda: (os.environ.clear(), os.environ.update(old)))
        deadline = time.time() + 5
        while "MARSHAL" not in self.tmux("capture-pane", "-p", "-t", "bw").stdout and time.time() < deadline:
            time.sleep(0.1)

    def tmux(self, *args):
        return subprocess.run(["tmux", *args], capture_output=True, text=True, env=self.env)

    def records(self, sizes):
        out = io.StringIO()
        with redirect_stdout(out):
            self.walk.main(["--target", "bw", "--out", os.path.join(self.dir.name, "cap"), "--sizes", sizes])
        return [json.loads(l) for l in out.getvalue().splitlines()]

    def test_walk_passes_on_a_board(self):
        recs = self.records("170x45,200x60")
        by = {r["id"]: r["status"] for r in recs}
        self.assertEqual(by.get("BW00-board-on-screen"), "PASS", recs)
        for cid in ["BW1-render-170x45", "BW20-help-opens", "BW21-help-closes", "BW22-focus-tasks",
                    "BW23-inspector-opens", "BW24-inspector-closes", "BW25-focus-lanes", "BW26-focus-inbox"]:
            self.assertEqual(by.get(cid), "PASS", (cid, recs))
        # The pane is resized to each width; the 170-column frame fits a
        # 170-column pane and, painted in a 200-column one, fits too.
        self.assertEqual(by.get("BW1-render-200x60"), "PASS", recs)
        self.assertTrue(os.path.isfile(os.path.join(self.dir.name, "cap", "board-170x45.txt")))
        # The window is given back at the size it was found.
        size = self.tmux("display-message", "-p", "-t", "bw", "#{window_width}x#{window_height}").stdout.strip()
        self.assertEqual(size, "220x60")

    def test_frame_wider_than_its_pane_fails(self):
        recs = self.records("100x30")
        by = {r["id"]: r for r in recs}
        self.assertEqual(by["BW1-render-100x30"]["status"], "FAIL", recs)
        self.assertIn("cells in a 100-cell pane", by["BW1-render-100x30"]["evidence"])

    def test_no_board_skips(self):
        self.tmux("respawn-pane", "-k", "-t", "bw", "sh", "-c", "echo plain shell; sleep 60")
        time.sleep(0.5)
        recs = self.records("120x40")
        self.assertEqual([(r["id"], r["status"]) for r in recs], [("BW00-board-on-screen", "SKIP")])


if __name__ == "__main__":
    unittest.main()
