"""Exercise the real installer with local release fixtures and an isolated PATH."""
import hashlib
import os
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]


class InstallDependencyTests(unittest.TestCase):
    def install(self, manager, present):
        with tempfile.TemporaryDirectory(prefix="marshal-install-") as directory:
            root = pathlib.Path(directory)
            commands = root / "commands"
            commands.mkdir()
            for name in ("tar", "gzip", "install", "sha256sum", "mktemp", "rm", "grep"):
                (commands / name).symlink_to(shutil.which(name))

            def stub(name, body):
                path = commands / name
                path.write_text("#!/bin/sh\n" + body + "\n")
                path.chmod(0o755)

            stub("uname", 'case "$1" in -s) echo Linux;; -m) echo x86_64;; esac')
            if manager:
                stub(manager, "exit 97")  # Running a manager must fail the test.
            for name in present:
                stub(name, "exit 98")  # Checking availability must not run tools.
            binary = root / "marshal"
            binary.write_text("fixture binary\n")
            archive = root / "marshal_0.0.5_linux_amd64.tar.gz"
            with tarfile.open(archive, "w:gz") as package:
                package.add(binary, arcname="marshal")
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            (root / "checksums.txt").write_text(f"{digest}  {archive.name}\n")
            stub("curl", '''destination=""
while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then shift; destination="$1"; fi
    url="$1"
    shift
done
case "$url" in
    */checksums.txt) /bin/cp "$FIXTURES/checksums.txt" "$destination" ;;
    */marshal_0.0.5_linux_amd64.tar.gz) /bin/cp "$FIXTURES/marshal_0.0.5_linux_amd64.tar.gz" "$destination" ;;
    *) exit 99 ;;
esac''')
            result = subprocess.run(
                ["/bin/sh", str(ROOT / "install.sh")],
                env={**os.environ, "PATH": str(commands), "FIXTURES": str(root),
                     "MARSHAL_VERSION": "v0.0.5", "MARSHAL_INSTALL_DIR": str(root / "installed")},
                capture_output=True, text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "installed/marshal").read_bytes(), binary.read_bytes())
            return [line for line in result.stderr.splitlines() if line.startswith("Missing ")]

    def test_package_managers_and_each_missing_tool(self):
        for manager in ("pacman", "apt", "apt-get", "dnf", "zypper", "brew"):
            for present in ((), ("tmux",), ("socat",), ("tmux", "socat")):
                with self.subTest(manager=manager, present=present):
                    lines = self.install(manager, present)
                    missing = [tool for tool in ("tmux", "socat") if tool not in present]
                    self.assertEqual(len(lines), len(missing))
                    for line, tool in zip(lines, missing):
                        command = (f"brew install {tool}" if manager == "brew" else
                                   f"sudo pacman -S {tool}" if manager == "pacman" else
                                   f"sudo {manager} install {tool}")
                        self.assertTrue(line.startswith(f"Missing {tool}:"))
                        self.assertTrue(line.endswith(command), line)
                        self.assertIn("requires it" if tool == "tmux" else "refused without it", line)

    def test_no_package_manager_is_nonfatal(self):
        lines = self.install(None, ())
        self.assertEqual(len(lines), 2)
        for line, tool in zip(lines, ("tmux", "socat")):
            self.assertTrue(line.endswith(f"install {tool} with your system's package manager"))


if __name__ == "__main__":
    unittest.main()
