import json
import pathlib
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.error
import urllib.request


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
                "-buildvcs=false",
                "-ldflags",
                "-X github.com/Zen1th53/marshal/internal/cli.Version=v1.0.1",
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

    def run_marshal(self, repo_path, *args):
        return subprocess.run(
            [str(self.marshal_bin), *args],
            cwd=repo_path,
            capture_output=True,
            text=True,
            check=True,
        )

    def wait_for_process(self, process, predicate, label):
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if process.poll() is not None:
                stdout, stderr = process.communicate()
                self.fail(
                    f"{label} exited early with {process.returncode}: "
                    f"stdout={stdout!r} stderr={stderr!r}"
                )
            if predicate():
                return
            time.sleep(0.05)
        self.fail(f"timed out waiting for {label}")

    def stop_process(self, process):
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=10)
        if process.stdout:
            process.stdout.close()
        if process.stderr:
            process.stderr.close()

    def start_daemon(self, repo_path):
        socket_path = repo_path / ".marshal" / "runtime.sock"
        if socket_path.exists():
            socket_path.unlink()
        process = subprocess.Popen(
            [str(self.marshal_bin), "daemon"],
            cwd=repo_path,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.wait_for_process(process, socket_path.exists, "daemon socket")
        return process

    def test_clean_user_lifecycle_isolated(self):
        with tempfile.TemporaryDirectory(prefix="clean-user-repo-") as repo_dir:
            repo_path = pathlib.Path(repo_dir)
            subprocess.run(["git", "init", "-b", "main"], cwd=repo_path, check=True)
            subprocess.run(
                ["git", "config", "user.name", "Test User"], cwd=repo_path, check=True
            )
            subprocess.run(
                ["git", "config", "user.email", "user@example.com"],
                cwd=repo_path,
                check=True,
            )
            (repo_path / "README.md").write_text(
                "# Test Repository\n", encoding="utf-8"
            )
            subprocess.run(["git", "add", "README.md"], cwd=repo_path, check=True)
            subprocess.run(
                ["git", "commit", "-m", "initial commit"], cwd=repo_path, check=True
            )

            version = self.run_marshal(repo_path, "version")
            self.assertIn("MARSHAL v1.0.1", version.stdout)

            initialized = self.run_marshal(repo_path, "init")
            self.assertIn("initialized", initialized.stdout)
            for name in (
                "CAPABILITIES.yaml",
                "PACK-VERSION.yaml",
                "RUNTIME-VERSION.yaml",
            ):
                self.assertTrue((repo_path / name).is_file())
            self.assertTrue((repo_path / ".marshal" / "state.db").is_file())

            doctor = self.run_marshal(repo_path, "doctor")
            self.assertIn("MARSHAL Doctor Verdict: PASS", doctor.stdout)
            self.assertIn("[NOT_RUN]  codex", doctor.stdout)

            daemon = self.start_daemon(repo_path)
            try:
                status = self.run_marshal(repo_path, "status")
                self.assertIn("tasks=0", status.stdout)

                agent = self.run_marshal(
                    repo_path,
                    "agent",
                    "register",
                    "--name",
                    "AutoDev",
                    "--role",
                    "developer",
                )
                self.assertTrue(agent.stdout.strip().startswith("AGENT-"))

                tasks_file = repo_path / "tasks.json"
                tasks_file.write_text(
                    json.dumps(
                        [
                            {
                                "id": "TASK-SMOKE-001",
                                "title": "Add smoke verification artifact",
                                "status": "ready",
                                "risk": "R1",
                            }
                        ]
                    ),
                    encoding="utf-8",
                )
                imported = self.run_marshal(repo_path, "task", "import", "tasks.json")
                self.assertIn("added=1", imported.stdout)
                shown = self.run_marshal(
                    repo_path, "task", "show", "TASK-SMOKE-001"
                )
                self.assertIn("TASK-SMOKE-001", shown.stdout)

                backup_file = repo_path / "smoke-backup.db"
                backup = self.run_marshal(
                    repo_path, "state", "backup", "--output", str(backup_file)
                )
                self.assertIn("Backup created", backup.stdout)
                verified = self.run_marshal(
                    repo_path, "state", "verify-backup", str(backup_file)
                )
                self.assertIn("Backup verified", verified.stdout)

                with socket.socket() as port_socket:
                    port_socket.bind(("127.0.0.1", 0))
                    port = port_socket.getsockname()[1]
                web = subprocess.Popen(
                    [
                        str(self.marshal_bin),
                        "web",
                        "serve",
                        "--listen",
                        "127.0.0.1",
                        "--port",
                        str(port),
                    ],
                    cwd=repo_path,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                )

                def web_responds():
                    try:
                        with urllib.request.urlopen(
                            f"http://127.0.0.1:{port}/", timeout=0.25
                        ) as response:
                            return response.status == 200 and b"MARSHAL" in response.read()
                    except (OSError, urllib.error.URLError):
                        return False

                try:
                    self.wait_for_process(web, web_responds, "web control plane")
                finally:
                    self.stop_process(web)
            finally:
                self.stop_process(daemon)

            restarted = self.start_daemon(repo_path)
            try:
                shown = self.run_marshal(
                    repo_path, "task", "show", "TASK-SMOKE-001"
                )
                self.assertIn("TASK-SMOKE-001", shown.stdout)
            finally:
                self.stop_process(restarted)


if __name__ == "__main__":
    unittest.main()
