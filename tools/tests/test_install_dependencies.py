"""Exercise the real installer with local release fixtures and an isolated PATH and HOME."""
import hashlib
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]

# The release binary is a stub that records what the installer asks of it.
MARSHAL_STUB = """#!/bin/sh
echo "$*" >> "$FIXTURES/marshal-calls"
case "$1" in
    version) echo "Marshal v0.0.5" ;;
    telemetry) echo "Anonymous telemetry: enabled; this installation is registered." ;;
esac
"""


class InstallTests(unittest.TestCase):
    def install(self, manager=None, present=(), uname=("Linux", "x86_64"), platform="linux_amd64",
                install_stub=False, tmux_version="3.4", env=None, on_path=False, runs=1, shell="/bin/bash"):
        directory = tempfile.mkdtemp(prefix="marshal-install-")
        self.addCleanup(shutil.rmtree, directory, True)
        root = pathlib.Path(directory)
        home = root / "home"
        home.mkdir()
        commands = root / "commands"
        commands.mkdir()
        for name in ("tar", "gzip", "install", "sha256sum", "mktemp", "rm", "grep", "mkdir", "sed", "head"):
            (commands / name).symlink_to(shutil.which(name))

        def stub(name, body):
            path = commands / name
            if path.is_symlink():
                path.unlink()
            path.write_text("#!/bin/sh\n" + body + "\n")
            path.chmod(0o755)

        stub("uname", f'case "$1" in -s) echo {uname[0]};; -m) echo {uname[1]};; esac')
        stub("id", "echo 1000")
        if install_stub:
            # BSD install (macOS) has no -D; reject it as macOS does.
            stub("install", f'''for a in "$@"; do case "$a" in -D*) echo "install: illegal option -- D" >&2; exit 64;; esac; done
exec {shutil.which("install")} "$@"''')
        tmux = f'echo "tmux {tmux_version}"'
        if manager:
            # A manager records that it ran and provides what it was asked for.
            stub(manager, f'''echo "$*" >> "$FIXTURES/manager-calls"
for a in "$@"; do case "$a" in
    tmux) printf '#!/bin/sh\\n{tmux}\\n' > "$COMMANDS/tmux"; /bin/chmod 755 "$COMMANDS/tmux" ;;
    git) printf '#!/bin/sh\\necho git version 2.50.0\\n' > "$COMMANDS/git"; /bin/chmod 755 "$COMMANDS/git" ;;
esac; done''')
        for name in present:
            stub(name, tmux if name == "tmux" else 'echo "git version 2.50.0"' if name == "git" else "exit 0")
        binary = root / "marshal"
        binary.write_text(MARSHAL_STUB)
        binary.chmod(0o755)
        archive = root / f"marshal_0.0.5_{platform}.tar.gz"
        with tarfile.open(archive, "w:gz") as package:
            package.add(binary, arcname="marshal")
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        (root / "checksums.txt").write_text(f"{digest}  {archive.name}\n")
        stub("curl", f'''destination=""
while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then shift; destination="$1"; fi
    url="$1"
    shift
done
case "$url" in
    */checksums.txt) /bin/cp "$FIXTURES/checksums.txt" "$destination" ;;
    */{archive.name}) /bin/cp "$FIXTURES/{archive.name}" "$destination" ;;
    *) exit 99 ;;
esac''')
        installed = root / "installed"
        path = str(commands) + (":" + str(installed) if on_path else "")
        environment = {"PATH": path, "HOME": str(home), "SHELL": shell, "FIXTURES": str(root),
                       "COMMANDS": str(commands), "MARSHAL_INSTALL_NONINTERACTIVE": "1",
                       "MARSHAL_VERSION": "v0.0.5", "MARSHAL_INSTALL_DIR": str(installed)}
        environment.update(env or {})
        for _ in range(runs):
            result = subprocess.run(["/bin/sh", str(ROOT / "install.sh")], env=environment,
                                    capture_output=True, text=True, stdin=subprocess.DEVNULL)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((installed / "marshal").read_bytes(), binary.read_bytes())
        self.assertEqual((installed / "marshal").stat().st_mode & 0o777, 0o755)
        self.assertIn("Start:\n", result.stderr)
        self.assertTrue(result.stderr.endswith("  cd /path/to/your/repository && marshal\n"), result.stderr)
        self.assertNotIn("marshal init", result.stderr)

        def read(name):
            p = root / name
            return p.read_text() if p.exists() else ""
        return result.stderr, read, home

    def test_bsd_install_without_dash_D(self):
        self.install(None, ("git", "tmux"), uname=("Darwin", "arm64"), platform="darwin_arm64",
                     install_stub=True)

    def test_install_registers_telemetry_once(self):
        out, read, _ = self.install(None, ("git", "tmux"))
        self.assertEqual(read("marshal-calls"), "version\ntelemetry register\n")
        self.assertIn("✓ Anonymous telemetry is on", out)
        self.assertIn("marshal telemetry disable", out)

    def test_path_is_added_once_to_the_shell_startup_file(self):
        for shell, rc in (("/bin/bash", ".bashrc"), ("/bin/zsh", ".zshrc"), ("/bin/sh", ".profile")):
            with self.subTest(shell=shell):
                out, _, home = self.install(None, ("git", "tmux", "claude"), shell=shell, runs=2)
                text = (home / rc).read_text()
                self.assertEqual(text.count("installed:$PATH"), 1, text)
                self.assertIn("is in", out)  # the second run finds it
                self.assertNotIn("Left to do:", out)
                self.assertIn("# only in this terminal", out)

    def test_path_left_alone_when_asked(self):
        out, _, home = self.install(None, ("git", "tmux"), env={"MARSHAL_NO_MODIFY_PATH": "1"})
        self.assertEqual(list(home.iterdir()), [])
        self.assertIn("Left to do:", out)
        self.assertIn("export PATH=", out)

    def test_path_already_set(self):
        out, _, home = self.install(None, ("git", "tmux", "claude"), on_path=True)
        self.assertEqual(list(home.iterdir()), [])
        self.assertIn("is on PATH", out)
        self.assertNotIn("Left to do:", out)

    def test_homebrew_installs_missing_tmux_and_git_without_asking(self):
        out, read, _ = self.install("brew", (), uname=("Darwin", "arm64"), platform="darwin_arm64")
        self.assertEqual(read("manager-calls"), "install git\ninstall tmux\n")
        self.assertIn("tmux 3.4 (installed)", out)
        self.assertIn("git version 2.50.0 (installed)", out)

    def test_sudo_managers_are_never_run_without_a_terminal(self):
        for manager, command in (("apt-get", "sudo apt-get install -y tmux"), ("apt", "sudo apt install -y tmux"),
                                 ("dnf", "sudo dnf install -y tmux"), ("zypper", "sudo zypper install -y tmux"),
                                 ("pacman", "sudo pacman -S --needed --noconfirm tmux")):
            with self.subTest(manager=manager):
                out, read, _ = self.install(manager, ("git",))
                self.assertEqual(read("manager-calls"), "")
                self.assertIn("! tmux is missing: the Marshal TUI runs in it", out)
                self.assertIn("Left to do:", out)
                self.assertIn(command, out)

    def test_deps_off_reports_only(self):
        out, read, _ = self.install("brew", ("git",), uname=("Darwin", "arm64"), platform="darwin_arm64",
                                    env={"MARSHAL_INSTALL_DEPS": "0"})
        self.assertEqual(read("manager-calls"), "")
        self.assertIn("brew install tmux", out)

    def test_old_tmux_is_reported(self):
        for version, ok in (("3.2a", False), ("3.3", False), ("3.3a", True), ("3.5", True),
                            ("next-3.6", True), ("4.0", True)):
            with self.subTest(version=version):
                out, _, _ = self.install(None, ("git", "tmux"), tmux_version=version)
                self.assertEqual("too old" in out, not ok, out)

    def test_no_package_manager_is_nonfatal(self):
        out, _, _ = self.install(None, ("git",))
        self.assertIn("install tmux with your system's package manager", out)

    def test_coding_agents_are_listed(self):
        out, _, _ = self.install(None, ("git", "tmux"))
        self.assertIn("No coding agent found", out)
        out, _, _ = self.install(None, ("git", "tmux", "claude", "codex"))
        self.assertIn("Coding agents: claude codex", out)


if __name__ == "__main__":
    unittest.main()
