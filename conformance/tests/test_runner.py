
import json
import tempfile
import unittest
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "conformance"))

import runner


class ConformanceRunnerTests(unittest.TestCase):
    def test_load_scenarios_requires_unique_ids(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "scenarios.json"
            p.write_text(json.dumps({
                "version": 1,
                "scenarios": [
                    {"id": "S-1", "fixture": "a", "expected": "PASS"},
                    {"id": "S-1", "fixture": "b", "expected": "DENY"},
                ],
            }))
            with self.assertRaises(ValueError):
                runner.load_scenarios(p)

    def test_validate_scenarios_rejects_missing_fixture(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            p = root / "scenarios.json"
            p.write_text(json.dumps({
                "version": 1,
                "scenarios": [
                    {"id": "S-1", "fixture": "missing", "expected": "PASS"}
                ],
            }))
            scenarios = runner.load_scenarios(p)
            errors = runner.validate_scenarios(scenarios, root / "fixtures")
            self.assertTrue(any("missing fixture" in e.lower() for e in errors))

    def test_validate_matrix_requires_all_core_adapters(self):
        matrix = {
            "version": 1,
            "adapters": {
                "gemini": {},
                "codex": {},
                "claude-code": {},
            },
        }
        errors = runner.validate_matrix(matrix)
        self.assertTrue(any("opencode" in e for e in errors))
        self.assertTrue(any("aider" in e for e in errors))
        self.assertTrue(any("crush" in e for e in errors))

    def test_detect_binary_returns_boolean_and_path(self):
        result = runner.detect_binary("python3")
        self.assertIsInstance(result["available"], bool)
        if result["available"]:
            self.assertTrue(result["path"])

    def test_pack_reference_validation_finds_missing_path(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "README.md").write_text("See `protocols/MISSING.md`")
            errors = runner.validate_markdown_references(root)
            self.assertTrue(any("protocols/MISSING.md" in e for e in errors))


    def test_pack_reference_validation_checks_interop_paths(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "README.md").write_text("See `interop/MISSING.md`")
            errors = runner.validate_markdown_references(root)
            self.assertTrue(any("interop/MISSING.md" in e for e in errors))


if __name__ == "__main__":
    unittest.main()
