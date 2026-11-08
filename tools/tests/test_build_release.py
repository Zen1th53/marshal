import hashlib
import json
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "tools" / "build_release.sh"
VERSION = "v0.2.0"
COMMIT = "0123456789abcdef"
BUILD_DATE = "2026-08-18T12:00:00Z"


class BuildReleaseTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source_directory = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.source_directory.cleanup)
        cls.root = pathlib.Path(cls.source_directory.name)
        try:
            tracked = subprocess.check_output(
                ["git", "ls-files", "-z"], cwd=ROOT
            ).decode("utf-8").split("\0")
        except (subprocess.CalledProcessError, FileNotFoundError):
            manifest_path = ROOT / "distribution" / "PACK-MANIFEST.json"
            if manifest_path.exists():
                data = json.loads(manifest_path.read_text("utf-8"))
                tracked = [entry["path"] for entry in data.get("files", [])]
            else:
                tracked = []
        for name in filter(None, tracked):
            target = cls.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / name, target)
        shutil.copy2(ROOT / "tools" / "regen_pack_manifest.sh", cls.root / "tools")
        subprocess.run(["git", "init", "-q"], cwd=cls.root, check=True)
        subprocess.run(["git", "add", "."], cwd=cls.root, check=True)
        manifest = cls.root / "distribution" / "PACK-MANIFEST.json"
        subprocess.run(["python3", str(cls.root / "tools" / "release_verify.py"), str(cls.root), str(manifest), "--generate"], check=True, capture_output=True)
        subprocess.run(["git", "add", "."], cwd=cls.root, check=True)

    def build(self, output: pathlib.Path) -> None:
        subprocess.run(
            ["bash", str(self.root / "tools" / SCRIPT.name), VERSION, COMMIT, BUILD_DATE, str(output)],
            cwd=self.root,
            check=True,
        )

    def test_builds_reproducible_archives_and_verifiable_metadata(self):
        with tempfile.TemporaryDirectory() as first_dir, tempfile.TemporaryDirectory() as second_dir:
            first = pathlib.Path(first_dir)
            second = pathlib.Path(second_dir)
            self.build(first)
            self.build(second)

            archive_names = [
                "marshal_0.2.0_linux_amd64.tar.gz",
                "marshal_0.2.0_linux_arm64.tar.gz",
            ]
            bundle_names = [
                "INSTALL.md",
                "LICENSE",
                "LICENSING.md",
                "README.md",
                "THIRD_PARTY_NOTICES.md",
                "marshal",
            ]
            for name in archive_names:
                archive = first / name
                self.assertTrue(archive.is_file(), archive)
                self.assertEqual(
                    hashlib.sha256(archive.read_bytes()).hexdigest(),
                    hashlib.sha256((second / name).read_bytes()).hexdigest(),
                )
                with tarfile.open(archive, "r:gz") as bundle:
                    self.assertEqual(sorted(bundle.getnames()), bundle_names)
                    for notice in ("LICENSING.md", "THIRD_PARTY_NOTICES.md"):
                        with bundle.extractfile(notice) as source:
                            self.assertEqual(source.read(), (ROOT / "docs" / "legal" / notice).read_bytes())
                    with bundle.extractfile("README.md") as source:
                        readme = source.read().decode("utf-8")
                    self.assertIn("(LICENSING.md)", readme)
                    self.assertIn("(THIRD_PARTY_NOTICES.md)", readme)
                    self.assertNotIn("(docs/legal/LICENSING.md)", readme)

            executable = first / "marshal"
            with tarfile.open(first / archive_names[0], "r:gz") as bundle:
                member = bundle.getmember("marshal")
                with bundle.extractfile(member) as source:
                    executable.write_bytes(source.read())
            executable.chmod(0o755)
            version = subprocess.run(
                [str(executable), "version"],
                check=True,
                capture_output=True,
                text=True,
            ).stdout
            self.assertIn("Marshal v0.2.0", version)
            self.assertIn("commit: 0123456789abcdef", version)
            executable.unlink()

            checksums = {}
            for line in (first / "checksums.txt").read_text(encoding="utf-8").splitlines():
                digest, name = line.split("  ", 1)
                checksums[name] = digest
            expected = archive_names + [
                "marshal_0.2.0_sbom.spdx.json",
                "build-metadata.json",
            ]
            for name in expected:
                self.assertEqual(
                    checksums[name], hashlib.sha256((first / name).read_bytes()).hexdigest()
                )

            sbom = json.loads(
                (first / "marshal_0.2.0_sbom.spdx.json").read_text(encoding="utf-8")
            )
            marshal_package = next(
                package
                for package in sbom["packages"]
                if package["name"] == "github.com/Zen1th53/marshal"
            )
            self.assertEqual(marshal_package["versionInfo"], VERSION)
            metadata = json.loads(
                (first / "build-metadata.json").read_text(encoding="utf-8")
            )
            self.assertEqual(metadata["version"], VERSION)
            self.assertEqual(metadata["commit"], COMMIT)

            for output in (first, second):
                subprocess.run(
                    [
                        "python3",
                        "tools/release_trust.py",
                        "generate-manifest",
                        "--dist",
                        str(output),
                        "--commit",
                        COMMIT,
                        "--version",
                        VERSION,
                        "--output",
                        str(output / "RELEASE-MANIFEST.json"),
                    ],
                    cwd=self.root,
                    check=True,
                )
                subprocess.run(
                    [
                        "python3",
                        "tools/release_trust.py",
                        "verify",
                        "--manifest",
                        str(output / "RELEASE-MANIFEST.json"),
                        "--dist",
                        str(output),
                    ],
                    cwd=self.root,
                    check=True,
                )
            self.assertEqual(
                (first / "RELEASE-MANIFEST.json").read_bytes(),
                (second / "RELEASE-MANIFEST.json").read_bytes(),
            )

    def test_rejects_invalid_build_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run(
                [
                    "bash",
                    str(SCRIPT),
                    VERSION,
                    COMMIT,
                    "not-a-date -X bad=value",
                    directory,
                ],
                cwd=ROOT,
                capture_output=True,
                text=True,
            )
            self.assertEqual(2, result.returncode)
            self.assertIn("invalid UTC build date", result.stderr)


if __name__ == "__main__":
    unittest.main()
