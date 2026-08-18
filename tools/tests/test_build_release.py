import hashlib
import pathlib
import subprocess
import tarfile
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "tools" / "build_release.sh"


class BuildReleaseTests(unittest.TestCase):
    def test_builds_supported_linux_archives_and_verifiable_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory)
            subprocess.run(
                [
                    "bash",
                    str(SCRIPT),
                    "v1.0.0-rc.1",
                    "0123456789abcdef",
                    "2026-08-18T12:00:00Z",
                    str(output),
                ],
                cwd=ROOT,
                check=True,
            )

            archives = [
                output / "marshal_1.0.0-rc.1_linux_amd64.tar.gz",
                output / "marshal_1.0.0-rc.1_linux_arm64.tar.gz",
            ]
            for archive in archives:
                self.assertTrue(archive.is_file(), archive)
                with tarfile.open(archive, "r:gz") as bundle:
                    self.assertEqual(
                        sorted(bundle.getnames()),
                        ["INSTALL.md", "LICENSE", "LICENSING.md", "marshal"],
                    )

            executable = output / "marshal"
            with tarfile.open(archives[0], "r:gz") as bundle:
                bundle.extract("marshal", path=output, filter="data")
            executable.chmod(0o755)
            version = subprocess.run(
                [str(executable), "version"], check=True, capture_output=True, text=True
            ).stdout
            self.assertIn("marshal v1.0.0-rc.1", version)
            self.assertIn("commit 0123456789abcdef", version)

            checksums = {}
            for line in (output / "checksums.txt").read_text(encoding="utf-8").splitlines():
                digest, name = line.split("  ", 1)
                checksums[name] = digest
            for archive in archives:
                self.assertEqual(
                    checksums[archive.name],
                    hashlib.sha256(archive.read_bytes()).hexdigest(),
                )
            self.assertTrue((output / "marshal_1.0.0-rc.1_sbom.cdx.json").is_file())
            self.assertTrue((output / "build-metadata.json").is_file())


if __name__ == "__main__":
    unittest.main()
