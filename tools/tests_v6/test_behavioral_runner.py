
import json
import tempfile
import unittest
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "conformance"))
import behavioral_runner

class BehavioralRunnerTests(unittest.TestCase):
    def test_mock_adapter_passes_expected_verdict(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            mock = root / "mock.py"
            mock.write_text(
                "import json; print(json.dumps({'status':'success','verdict':'DENY','actions':[]}))"
            )
            result = behavioral_runner.run_adapter(
                [sys.executable, str(mock)], expected="DENY", timeout=10
            )
            self.assertEqual(result["status"], "PASS")

    def test_nonzero_adapter_is_fail(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            mock = root / "mock.py"
            mock.write_text("raise SystemExit(3)")
            result = behavioral_runner.run_adapter(
                [sys.executable, str(mock)], expected="PASS", timeout=10
            )
            self.assertEqual(result["status"], "FAIL")

if __name__ == "__main__":
    unittest.main()
