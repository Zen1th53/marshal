
import json
import tempfile
import unittest
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))

import agentos


class AgentOSTests(unittest.TestCase):
    def test_detect_project_finds_python_github_actions_and_agents(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "pyproject.toml").write_text("[project]\nname='demo'\n")
            (root / "AGENTS.md").write_text("# rules\n")
            wf = root / ".github" / "workflows"
            wf.mkdir(parents=True)
            (wf / "test.yml").write_text("name: test\n")
            result = agentos.detect_project(root)
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
        conflicts = agentos.reconcile_state(file_state, runtime_state)
        fields = {c["field"] for c in conflicts}
        self.assertIn("task.id", fields)
        self.assertIn("project.commit", fields)

    def test_read_pack_version(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "PACK-VERSION.yaml").write_text('pack_version: "5.0.0"\nschema_version: 1\n')
            result = agentos.read_pack_version(root)
            self.assertEqual(result["pack_version"], "5.0.0")
            self.assertEqual(result["schema_version"], 1)


if __name__ == "__main__":
    unittest.main()
