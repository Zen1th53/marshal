import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]


class PackManifestRegenTests(unittest.TestCase):
    def test_rewrites_stale_manifest_and_verifies(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory) / "source tree"
            root.mkdir()
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
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(ROOT / name, target)
            # Include the new entry point before the runtime commits this task.
            script = root / "tools" / "regen_pack_manifest.sh"
            shutil.copy2(ROOT / "tools" / script.name, script)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(
                ["git", "-c", "user.name=Manifest Test", "-c",
                 "user.email=manifest-test@example.invalid", "-c",
                 "commit.gpgsign=false", "commit", "-qm", "fixture"],
                cwd=root,
                check=True,
            )
            manifest = root / "distribution" / "PACK-MANIFEST.json"
            verify = ["python3", str(root / "tools" / "release_verify.py"),
                      str(root), str(manifest)]
            # Establish a valid baseline independently of the entry point.
            subprocess.run(verify + ["--generate"], check=True, capture_output=True)
            previous = manifest.read_bytes()
            metadata = json.loads(previous)
            with (root / "README.md").open("a", encoding="utf-8") as source:
                source.write("\nManifest regeneration test.\n")
            stale = subprocess.run(verify, capture_output=True, text=True)
            self.assertEqual(stale.returncode, 1)
            self.assertIn("hash mismatch: README.md", stale.stdout)

            # Exercise explicit root and the script-relative default from elsewhere.
            for arguments in ([str(root)], []):
                with (root / "README.md").open("a", encoding="utf-8") as source:
                    source.write("\nAnother tracked change.\n")
                subprocess.run(["sh", str(script), *arguments], cwd=directory,
                               check=True, capture_output=True)
                self.assertNotEqual(previous, manifest.read_bytes())
                subprocess.run(verify, check=True, capture_output=True)
                regenerated = json.loads(manifest.read_bytes())
                for key in ("pack_version", "generated_date", "excluded_paths"):
                    self.assertEqual(metadata[key], regenerated[key])
                previous = manifest.read_bytes()

    def test_build_scripts_do_not_mutate_manifest_during_build(self):
        for name in ("build_release.sh", "build_extreme.sh"):
            with self.subTest(script=name):
                source = (ROOT / "tools" / name).read_text(encoding="utf-8")
                self.assertNotIn("regen_pack_manifest.sh", source)
        release_source = (ROOT / "tools" / "build_release.sh").read_text(encoding="utf-8")
        self.assertIn("tools/release_verify.py", release_source)
        self.assertLess(release_source.index("tools/release_verify.py"), release_source.index("go build"))


if __name__ == "__main__":
    unittest.main()
