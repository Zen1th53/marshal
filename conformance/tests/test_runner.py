
from contextlib import redirect_stdout
import io
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
                "antigravity": {},
                "codex": {},
                "claude-code": {},
            },
        }
        errors = runner.validate_matrix(matrix)
        self.assertTrue(any("opencode" in e for e in errors))
        self.assertTrue(any("aider" in e for e in errors))
        self.assertTrue(any("crush" in e for e in errors))

    def test_validate_matrix_requires_antigravity_not_gemini(self):
        matrix = {
            "version": 1,
            "adapters": {
                "gemini": {},
                "codex": {},
                "claude-code": {},
                "opencode": {},
                "aider": {},
                "crush": {},
            },
        }
        self.assertEqual(runner.validate_matrix(matrix), ["missing core adapter: antigravity"])
        matrix["adapters"]["antigravity"] = {"binary": "agy"}
        del matrix["adapters"]["gemini"]
        self.assertEqual(runner.validate_matrix(matrix), [])

    def test_probe_defaults_to_agy_binary(self):
        buf = io.StringIO()
        args = runner.build_parser().parse_args(["probe-adapters"])
        with redirect_stdout(buf):
            self.assertEqual(args.func(args), 0)
        probed = [item["name"] for item in json.loads(buf.getvalue())]
        self.assertIn("agy", probed)
        self.assertNotIn("gemini", probed)

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

    def test_pack_reference_validation_accepts_file_relative_templates(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            skill = root / ".agents" / "skills" / "example"
            (skill / "templates").mkdir(parents=True)
            (skill / "templates" / "report.md").write_text("Report template")
            (skill / "SKILL.md").write_text("Use `templates/report.md`")
            self.assertEqual(runner.validate_markdown_references(root), [])
            (skill / "SKILL.md").write_text("Use `templates/missing.md`")
            self.assertTrue(runner.validate_markdown_references(root))


    def test_pack_reference_validation_checks_interop_paths(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "README.md").write_text("See `interop/MISSING.md`")
            errors = runner.validate_markdown_references(root)
            self.assertTrue(any("interop/MISSING.md" in e for e in errors))

    def test_markdown_relative_links_validated(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "README.md").write_text("See [missing](docs/missing.md) and [ok](target.md#heading) and [ext](https://example.com)")
            (root / "target.md").write_text("# Target\n")
            errors = runner.validate_markdown_references(root)
            self.assertEqual(len(errors), 1)
            self.assertIn("docs/missing.md", errors[0])

    def test_schema_sample_validation(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "schemas" / "samples").mkdir(parents=True)
            schema = {"type": "object", "required": ["id"], "properties": {"id": {"type": "string"}}}
            (root / "schemas" / "item.schema.json").write_text(json.dumps(schema))
            (root / "schemas" / "samples" / "item.json").write_text(json.dumps({"id": "valid"}))
            errors = runner.validate_schemas(root)
            self.assertEqual(errors, [])

            (root / "schemas" / "samples" / "item.json").write_text(json.dumps({"id": 123}))
            errors = runner.validate_schemas(root)
            self.assertTrue(len(errors) > 0)


if __name__ == "__main__":
    unittest.main()
