import json
import os
import pathlib
import shutil
import signal
import subprocess
import tempfile
import time
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]


class CleanInstallSmokeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.bin_dir = tempfile.mkdtemp(prefix="marshal-bin-")
        cls.marshal_bin = pathlib.Path(cls.bin_dir) / "marshal"
        subprocess.run(
            [
                "go",
                "build",
                "-ldflags",
                "-X github.com/Zen1th53/marshal/internal/version.Version=v1.0.1",
                "-o",
                str(cls.marshal_bin),
                "./cmd/marshal",
            ],
            cwd=ROOT,
            check=True,
        )

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.bin_dir, ignore_errors=True)

    def test_clean_user_lifecycle_isolated(self):
        with tempfile.TemporaryDirectory(prefix="clean-user-repo-") as repo_dir:
            repo_path = pathlib.Path(repo_dir)

            # Initialize a clean git repository without any marshal files
            subprocess.run(["git", "init", "-b", "main"], cwd=repo_path, check=True)
            subprocess.run(["git", "config", "user.name", "Test User"], cwd=repo_path, check=True)
            subprocess.run(["git", "config", "user.email", "user@example.com"], cwd=repo_path, check=True)
            (repo_path / "README.md").write_text("# Test Repository\n", encoding="utf-8")
            subprocess.run(["git", "add", "README.md"], cwd=repo_path, check=True)
            subprocess.run(["git", "commit", "-m", "initial commit"], cwd=repo_path, check=True)

            # 1. marshal version
            res = subprocess.run([str(self.marshal_bin), "version"], cwd=repo_path, capture_output=True, text=True, check=True)
            self.assertIn("marshal v1.0.1", res.stdout)

            # 2. marshal init (must create all required contracts self-contained)
            res = subprocess.run([str(self.marshal_bin), "init"], cwd=repo_path, capture_output=True, text=True, check=True)
            self.assertIn("initialized", res.stdout)
            self.assertTrue((repo_path / "CAPABILITIES.yaml").is_file())
            self.assertTrue((repo_path / "PACK-VERSION.yaml").is_file())
            self.assertTrue((repo_path / "RUNTIME-VERSION.yaml").is_file())
            self.assertTrue((repo_path / ".marshal" / "state.db").is_file())

            # 3. marshal doctor
            res = subprocess.run([str(self.marshal_bin), "doctor"], cwd=repo_path, capture_output=True, text=True, check=True)
            self.assertIn("MARSHAL Doctor Verdict: PASS", res.stdout)

            # 4. Start daemon in background
            daemon_proc = subprocess.Popen(
                [str(self.marshal_bin), "daemon"],
                cwd=repo_path,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            time.sleep(0.5)
            self.assertIsNone(daemon_proc.poll(), "daemon crashed prematurely")

            try:
                # 5. marshal status
                res = subprocess.run([str(self.marshal_bin), "status"], cwd=repo_path, capture_output=True, text=True, check=True)
                self.assertIn("tasks=0", res.stdout)

                # 6. Register agent
                res = subprocess.run(
                    [str(self.marshal_bin), "agent", "register", "--name", "AutoDev", "--role", "developer"],
                    cwd=repo_path,
                    capture_output=True,
                    text=True,
                    check=True,
                )
                self.assertTrue(res.stdout.strip().startswith("AGENT-"))

                # 7. Import task
                tasks_file = repo_path / "tasks.json"
                tasks_file.write_text(json.dumps([
                    {
                        "id": "TASK-SMOKE-001",
                        "title": "Add smoke verification artifact",
                        "status": "ready",
                        "risk": "R1",
                    }
                ]), encoding="utf-8")

                res = subprocess.run(
                    [str(self.marshal_bin), "task", "import", "tasks.json"],
                    cwd=repo_path,
                    capture_output=True,
                    text=True,
                    check=True,
                )
                self.assertIn("added=1", res.stdout)

                # 8. Check task state
                res = subprocess.run(
                    [str(self.marshal_bin), "task", "show", "TASK-SMOKE-001"],
                    cwd=repo_path,
                    capture_output=True,
                    text=True,
                    check=True,
                )
                self.assertIn("TASK-SMOKE-001", res.stdout)

                # 9. Online backup
                backup_file = repo_path / "smoke-backup.db"
                res = subprocess.run(
                    [str(self.marshal_bin), "state", "backup", "--output", str(backup_file)],
                    cwd=repo_path,
                    capture_output=True,
                    text=True,
                    check=True,
                )
                self.assertIn("Backup created", res.stdout)

                # 10. Verify backup
                res = subprocess.run(
                    [str(self.marshal_bin), "state", "verify-backup", str(backup_file)],
                    cwd=repo_path,
                    capture_output=True,
                    text=True,
                    check=True,
                )
                self.assertIn("Backup verified", res.stdout)

            finally:
                # 11. Clean shutdown of daemon
                daemon_proc.send_signal(signal.SIGTERM)
                daemon_proc.wait(timeout=5)
                if daemon_proc.stdout:
                    daemon_proc.stdout.close()
                if daemon_proc.stderr:
                    daemon_proc.stderr.close()

            # 12. Restart daemon and check state persistence
            daemon_proc2 = subprocess.Popen(
                [str(self.marshal_bin), "daemon"],
                cwd=repo_path,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            time.sleep(0.5)
            self.assertIsNone(daemon_proc2.poll(), "daemon restart crashed")
            try:
                res = subprocess.run(
                    [str(self.marshal_bin), "task", "show", "TASK-SMOKE-001"],
                    cwd=repo_path,
                    capture_output=True,
                    text=True,
                    check=True,
                )
                self.assertIn("TASK-SMOKE-001", res.stdout)
            finally:
                daemon_proc2.send_signal(signal.SIGTERM)
                daemon_proc2.wait(timeout=5)
                if daemon_proc2.stdout:
                    daemon_proc2.stdout.close()
                if daemon_proc2.stderr:
                    daemon_proc2.stderr.close()


if __name__ == "__main__":
    unittest.main()
