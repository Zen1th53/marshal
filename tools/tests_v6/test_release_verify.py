
import hashlib
import json
import tempfile
import unittest
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
import release_verify

class ReleaseVerifyTests(unittest.TestCase):
    def test_manifest_hash_verifies(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            f = root / "a.txt"
            f.write_text("abc")
            digest = hashlib.sha256(f.read_bytes()).hexdigest()
            manifest = {"files":[{"path":"a.txt","sha256":digest}]}
            self.assertEqual(release_verify.verify_manifest(root, manifest), [])

    def test_manifest_detects_tamper(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "a.txt").write_text("changed")
            manifest = {"files":[{"path":"a.txt","sha256":"0"*64}]}
            self.assertTrue(release_verify.verify_manifest(root, manifest))

    def test_generate_manifest_is_sorted_complete_and_reproducible(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "b.txt").write_bytes(b"b")
            (root / "a.txt").write_bytes(b"alpha")
            manifest = release_verify.generate_manifest(
                root,
                ["b.txt", "excluded.json", "a.txt"],
                pack_version="6.0.0",
                generated_date="2026-08-11",
                excluded_paths=["excluded.json"],
            )
            self.assertEqual([item["path"] for item in manifest["files"]], ["a.txt", "b.txt"])
            self.assertEqual(manifest["files"][0]["bytes"], 5)
            self.assertEqual(release_verify.verify_manifest(root, manifest, ["a.txt", "b.txt", "excluded.json"]), [])

    def test_verify_detects_unlisted_tracked_file(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "a.txt").write_text("a")
            manifest = {"excluded_paths": [], "files": []}
            self.assertIn("unlisted: a.txt", release_verify.verify_manifest(root, manifest, ["a.txt"]))

if __name__ == "__main__":
    unittest.main()
