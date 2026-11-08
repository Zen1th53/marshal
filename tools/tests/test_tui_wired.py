import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE_PATH = ROOT / "tools" / "e2e" / "tui_walk.py"


def load_walk():
    spec = importlib.util.spec_from_file_location("tui_walk", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


MATRIX = """# matrix

| ID | Type | Path | State | Detail |
|---|---|---|---|---|
| CTUI-0001 | ACTION | A / Form | FORM | ▸ Task *: (empty) |
| CTUI-0002 | ACTION | A / Blocked | RESULT | BLOCKED: no run is active |
| CTUI-0003 | ACTION | A / Precondition | DISABLED | cannot start x: there is no goal yet |
| CTUI-0004 | ACTION | A / Unwired | DISABLED | learning service owns this, and it requires a \\| b |
| CTUI-0005 | ACTION | A / Bug | ERROR | BLOCKED: invalid input: unsupported count table |
| CTUI-0006 | ACTION | A / Noise | RESULT | refreshed |
"""


class WiredTests(unittest.TestCase):
    def setUp(self):
        self.walk = load_walk()
        self.dir = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.dir.name)
        (self.root / "matrix.md").write_text(MATRIX, encoding="utf-8")
        specs = [{"spec_id": "CTUI-%04d" % i, "process_association": "Process 07"} for i in range(8)]
        (self.root / "manifest.json").write_text(json.dumps({"specs": specs}), encoding="utf-8")
        (self.root / "tasks").mkdir()

    def tearDown(self):
        self.dir.cleanup()

    def build(self):
        return self.walk.build_wired(str(self.root), "matrix.md", "manifest.json", "tasks")

    def test_walk_states_map_to_wired_status(self):
        got = {e["spec_id"]: e for e in self.build()["specs"]}
        self.assertEqual("live", got["CTUI-0000"]["status"])  # not walked
        self.assertEqual("live", got["CTUI-0001"]["status"])
        self.assertEqual("blocked_by_state", got["CTUI-0002"]["status"])
        self.assertEqual("blocked_by_state", got["CTUI-0003"]["status"])
        unwired = got["CTUI-0004"]
        self.assertEqual("under_construction", unwired["status"])
        self.assertEqual("learning service", unwired["owner"])
        self.assertEqual(self.walk.PHASE_UNWIRED, unwired["phase"])
        self.assertIn("a | b", unwired["cause"])
        bug = got["CTUI-0005"]
        self.assertEqual("under_construction", bug["status"])
        self.assertTrue(bug["cause"].startswith("bug: "))
        self.assertEqual(self.walk.PHASE_BUG, bug["phase"])
        self.assertEqual("Process 07", bug["owner"])
        noise = got["CTUI-0006"]
        self.assertNotEqual("refreshed", noise["cause"])
        for e in got.values():
            if e["status"] == "under_construction":
                self.assertTrue(e["owner"] and e["cause"] and e["phase"], e)

    def test_broken_walk_state_is_refused(self):
        (self.root / "matrix.md").write_text(MATRIX + "| CTUI-0007 | ACTION | A / Dead | DEAD | x |\n",
                                             encoding="utf-8")
        with self.assertRaises(ValueError):
            self.build()

    def test_refusal_without_a_reason_is_refused(self):
        # A DISABLED entry whose only detail is noise names no reason; the
        # walk missed it and must be re-walked rather than recorded.
        (self.root / "matrix.md").write_text(MATRIX + "| CTUI-0007 | ACTION | A / Silent | DISABLED | refreshed |\n",
                                             encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "CTUI-0007 was refused"):
            self.build()

    def test_regression_from_live_needs_a_task_file(self):
        old = {"specs": [{"spec_id": "CTUI-0001", "status": "live"},
                         {"spec_id": "CTUI-0004", "status": "under_construction"}]}
        new = {"specs": [{"spec_id": "CTUI-0001", "status": "under_construction", "task": ""},
                         {"spec_id": "CTUI-0004", "status": "under_construction", "task": ""}]}
        bad = self.walk.regressions(str(self.root), old, new)
        self.assertEqual(1, len(bad))
        self.assertIn("CTUI-0001", bad[0])

        (self.root / "tasks" / "T-1.md").write_text("Unwire CTUI-0001 until X.\n", encoding="utf-8")
        (self.root / "matrix.md").write_text(MATRIX.replace("| CTUI-0001 | ACTION | A / Form | FORM |",
                                                            "| CTUI-0001 | ACTION | A / Form | DISABLED |"),
                                             encoding="utf-8")
        wired = self.build()
        self.assertEqual("tasks/T-1.md", wired["specs"][1]["task"])
        self.assertEqual([], self.walk.regressions(str(self.root), old, wired))

    def test_committed_wired_json_is_current(self):
        if not (ROOT / "tasks").is_dir():
            self.skipTest("task specs are not part of this source release")
        out = ROOT / "internal" / "tui" / "wired.json"
        committed = json.loads(out.read_text(encoding="utf-8"))
        wired = self.walk.build_wired(str(ROOT), committed["source"], "internal/tui/manifest.json", "tasks")
        self.assertEqual(out.read_text(encoding="utf-8"), self.walk.dump_wired(wired))


if __name__ == "__main__":
    unittest.main()
