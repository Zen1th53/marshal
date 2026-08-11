import json
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "conformance" / "runtime_runner.py"


class RuntimeRunnerTests(unittest.TestCase):
    def test_supported_scenario_runs_executable_invariant(self):
        proc = subprocess.run(
            [sys.executable, str(RUNNER), "--root", str(ROOT), "--scenario", "CONF-003"],
            text=True,
            capture_output=True,
            timeout=120,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        payload = json.loads(proc.stdout)
        self.assertEqual(payload["status"], "PASS")
        self.assertEqual(payload["scenario_id"], "CONF-003")
        self.assertEqual(payload["verdict"], "BLOCKED")
        self.assertEqual(payload["exit_code"], 0)
        self.assertTrue(payload["command"])
        self.assertTrue(payload["current_commit"])
        self.assertIn("exactly one", payload["observed_invariant"])

    def test_unsupported_scenario_is_explicitly_not_run(self):
        proc = subprocess.run(
            [sys.executable, str(RUNNER), "--root", str(ROOT), "--scenario", "CONF-020"],
            text=True,
            capture_output=True,
            timeout=30,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        payload = json.loads(proc.stdout)
        self.assertEqual(payload["status"], "NOT_RUN")
        self.assertNotIn("verdict", payload)


if __name__ == "__main__":
    unittest.main()
