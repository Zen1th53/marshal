import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE_PATH = ROOT / "tools" / "release_trust.py"


def load_release_trust():
    spec = importlib.util.spec_from_file_location("release_trust", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ReleaseTrustTests(unittest.TestCase):
    def test_manifest_verifies_archives_sbom_and_checksums(self):
        trust = load_release_trust()
        with tempfile.TemporaryDirectory() as directory:
            dist = pathlib.Path(directory)
            artifact = dist / "marshal_1.5.0_linux_amd64.tar.gz"
            artifact.write_bytes(b"archive")
            sbom = dist / "marshal_1.5.0_sbom.spdx.json"
            sbom.write_text("{}\n", encoding="utf-8")
            digest = trust.compute_sha256(artifact)
            sbom_digest = trust.compute_sha256(sbom)
            (dist / "checksums.txt").write_text(
                f"{digest}  {artifact.name}\n{sbom_digest}  {sbom.name}\n",
                encoding="utf-8",
            )
            manifest = trust.generate_release_manifest(
                dist, "0123456789abcdef", "v1.5.0"
            )
            manifest_path = dist / "RELEASE-MANIFEST.json"
            manifest_path.write_text(json.dumps(manifest), encoding="utf-8")

            self.assertEqual("marshal.release-manifest.v1", manifest["schema_version"])
            self.assertEqual([], trust.verify_release(manifest_path, dist))

            artifact.write_bytes(b"tampered")
            errors = trust.verify_release(manifest_path, dist)
            self.assertTrue(any("checksum mismatch" in error for error in errors))
            self.assertTrue(any("checksums.txt mismatch" in error for error in errors))

    def test_manifest_rejects_unsafe_checksum_path(self):
        trust = load_release_trust()
        with tempfile.TemporaryDirectory() as directory:
            dist = pathlib.Path(directory)
            (dist / "checksums.txt").write_text(
                f"{'0' * 64}  ../outside\n", encoding="utf-8"
            )
            manifest = trust.generate_release_manifest(dist, "01234567", "v1.5.0")
            manifest_path = dist / "RELEASE-MANIFEST.json"
            manifest_path.write_text(json.dumps(manifest), encoding="utf-8")

            self.assertIn(
                "unsafe checksum path: ../outside",
                trust.verify_release(manifest_path, dist),
            )


if __name__ == "__main__":
    unittest.main()
