import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE_PATH = ROOT / "tools" / "generate_sbom.py"


def load_generator():
    spec = importlib.util.spec_from_file_location("generate_sbom", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class GenerateSBOMTests(unittest.TestCase):
    def test_build_bom_is_deterministic_and_sorted(self):
        generator = load_generator()
        modules = [
            {
                "Path": "github.com/Zen1th53/marshal",
                "Main": True,
                "GoVersion": "1.25.0",
            },
            {
                "Path": "z.example/module",
                "Version": "v1.2.3",
                "Sum": "h1:zsum",
            },
            {
                "Path": "a.example/module",
                "Version": "v2.0.0",
                "Sum": "h1:asum",
            },
        ]

        first = generator.build_bom(
            modules,
            product_version="v1.0.0-rc.1",
            commit="0123456789abcdef",
            build_time="2026-08-18T12:00:00Z",
        )
        second = generator.build_bom(
            list(reversed(modules)),
            product_version="v1.0.0-rc.1",
            commit="0123456789abcdef",
            build_time="2026-08-18T12:00:00Z",
        )

        self.assertEqual(first, second)
        self.assertEqual(first["bomFormat"], "CycloneDX")
        self.assertEqual(first["specVersion"], "1.5")
        self.assertEqual(first["metadata"]["component"]["version"], "1.0.0-rc.1")
        self.assertEqual(
            [component["name"] for component in first["components"]],
            ["a.example/module", "z.example/module"],
        )
        self.assertEqual(
            first["metadata"]["properties"],
            [
                {"name": "marshal:commit", "value": "0123456789abcdef"},
                {"name": "marshal:go-version", "value": "1.25.0"},
            ],
        )

    def test_parse_concatenated_go_module_json(self):
        generator = load_generator()
        payload = '{"Path":"example/main","Main":true}\n{"Path":"example/dep","Version":"v1.0.0"}\n'

        self.assertEqual(
            generator.parse_modules(payload),
            [
                {"Path": "example/main", "Main": True},
                {"Path": "example/dep", "Version": "v1.0.0"},
            ],
        )

    def test_write_bom_uses_canonical_json(self):
        generator = load_generator()
        with tempfile.TemporaryDirectory() as directory:
            destination = pathlib.Path(directory) / "sbom.json"
            generator.write_bom(destination, {"z": 1, "a": 2})
            self.assertEqual(
                destination.read_text(encoding="utf-8"),
                '{\n  "a": 2,\n  "z": 1\n}\n',
            )


if __name__ == "__main__":
    unittest.main()
