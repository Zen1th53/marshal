
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

if __name__ == "__main__":
    unittest.main()
