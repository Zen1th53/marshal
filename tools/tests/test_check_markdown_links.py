import importlib.util
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).parents[1] / "check_markdown_links.py"


def load_module():
    spec = importlib.util.spec_from_file_location("check_markdown_links", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class MarkdownLinkTests(unittest.TestCase):
    def test_accepts_existing_relative_link_and_anchor(self):
        checker = load_module()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "README.md").write_text("[Guide](docs/guide.md#setup)\n", encoding="utf-8")
            (root / "docs").mkdir()
            (root / "docs" / "guide.md").write_text("# Setup\n", encoding="utf-8")
            self.assertEqual([], checker.check_tree(root))

    def test_reports_missing_file_and_anchor(self):
        checker = load_module()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "README.md").write_text(
                "[Missing](missing.md) [Bad anchor](guide.md#absent)\n", encoding="utf-8"
            )
            (root / "guide.md").write_text("# Present\n", encoding="utf-8")
            failures = checker.check_tree(root)
            self.assertEqual(2, len(failures))


if __name__ == "__main__":
    unittest.main()
