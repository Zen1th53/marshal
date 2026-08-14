
import json
import importlib.util
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("marshal_tool", ROOT / "tools" / "marshal.py")
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cannot load tools/marshal.py")
marshal_tool = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(marshal_tool)


class MarshalTests(unittest.TestCase):
    def test_detect_project_finds_python_github_actions_and_agents(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "pyproject.toml").write_text("[project]\nname='demo'\n")
            (root / "AGENTS.md").write_text("# rules\n")
            wf = root / ".github" / "workflows"
            wf.mkdir(parents=True)
            (wf / "test.yml").write_text("name: test\n")
            result = marshal_tool.detect_project(root)
            self.assertIn("python", result["stacks"])
            self.assertIn("github-actions", result["ci"])
            self.assertIn("AGENTS.md", result["instruction_files"])

    def test_reconcile_state_detects_commit_and_task_conflict(self):
        file_state = {
            "task": {"id": "TASK-1"},
            "project": {"commit": "aaa"},
        }
        runtime_state = {
            "task": {"id": "TASK-2"},
            "project": {"commit": "bbb"},
        }
        conflicts = marshal_tool.reconcile_state(file_state, runtime_state)
        fields = {c["field"] for c in conflicts}
        self.assertIn("task.id", fields)
        self.assertIn("project.commit", fields)

    def test_read_pack_version(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "PACK-VERSION.yaml").write_text('pack_version: "5.0.0"\nschema_version: 1\n')
            result = marshal_tool.read_pack_version(root)
            self.assertEqual(result["pack_version"], "5.0.0")
            self.assertEqual(result["schema_version"], 1)


if __name__ == "__main__":
    unittest.main()
