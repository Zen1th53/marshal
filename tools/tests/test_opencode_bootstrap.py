import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("bootstrap", Path(__file__).parents[1] / "prepare_opencode_bootstrap.py")
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)

class OpenCodeBootstrapTests(unittest.TestCase):
    def test_lock_pins_patched_compatible_toml_in_fresh_revision(self):
        lock = json.loads(bootstrap.LOCK.read_text())
        toml = next(p for p in lock["packages"] if p["name"] == "toml")
        self.assertEqual(toml["version"], "4.2.0")
        self.assertEqual(lock["bootstrap_revision"], "1.18.16-toml4.2.0")

    def test_archive_rejects_traversal_and_links(self):
        for name, kind in [("package/../../escaped", tarfile.REGTYPE), ("/package/file", tarfile.REGTYPE), ("package/link", tarfile.SYMTYPE), ("package/link", tarfile.LNKTYPE)]:
            with self.subTest(name=name, kind=kind), tempfile.TemporaryDirectory() as temporary:
                raw = io.BytesIO()
                with tarfile.open(fileobj=raw, mode="w:gz") as archive:
                    entry = tarfile.TarInfo(name)
                    entry.type = kind
                    entry.linkname = "/etc/passwd"
                    archive.addfile(entry)
                with self.assertRaises(ValueError):
                    bootstrap.unpack(raw.getvalue(), Path(temporary) / "package")

    def test_digest_has_deterministic_cross_language_format(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            file = root / "opencode/package.json"
            file.parent.mkdir()
            file.write_bytes(b"{}")
            expected = hashlib.sha256(b"opencode/package.json\0" + hashlib.sha256(b"{}").hexdigest().encode() + b"\n").hexdigest()
            self.assertEqual(bootstrap.tree_digest(root), expected)
            (file.parent / "opencode.json").write_bytes(b"{}")
            with self.assertRaises(ValueError):
                bootstrap.tree_digest(root)

    def test_digest_sorts_flat_relative_paths_like_go_runtime(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            contents = {"opencode/node_modules/pkg/x.js": b"first", "opencode/node_modules/pkg/x/child.js": b"second"}
            for relative, data in contents.items():
                file = root / relative
                file.parent.mkdir(parents=True, exist_ok=True)
                file.write_bytes(data)
            tree = hashlib.sha256()
            for relative in sorted(contents):
                tree.update(relative.encode() + b"\0" + hashlib.sha256(contents[relative]).hexdigest().encode() + b"\n")
            self.assertEqual(bootstrap.tree_digest(root), tree.hexdigest())

    def test_existing_bundle_must_match_exact_pinned_contents(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config = root / "opencode"
            config.mkdir()
            (config / "package.json").write_bytes(b"{}")
            lock = {"tree_sha256": bootstrap.tree_digest(root)}
            bootstrap.prepare(root, lock)
            (config / "package.json").write_bytes(b"tampered")
            with self.assertRaisesRegex(ValueError, "differs from pinned"):
                bootstrap.prepare(root, lock)

    def test_lock_has_exact_official_version_and_integrity_pins(self):
        lock = json.loads(bootstrap.LOCK.read_text())
        self.assertEqual(lock["version"], "1.18.16")
        packages = {entry["name"]: entry for entry in lock["packages"]}
        self.assertEqual(packages["@opencode-ai/plugin"]["version"], lock["version"])
        self.assertEqual(packages["@opencode-ai/sdk"]["version"], lock["version"])
        for package in packages.values():
            self.assertTrue(package["url"].startswith("https://registry.npmjs.org/" + package["name"] + "/-/"))
            self.assertTrue(package["integrity"].startswith("sha512-"))
        source = (Path(__file__).parents[2] / "internal/app/opencode_bootstrap_extreme.go").read_text()
        self.assertIn('opencodeBootstrapTreeSHA256 = "' + lock["tree_sha256"] + '"', source)

if __name__ == "__main__":
    unittest.main()
