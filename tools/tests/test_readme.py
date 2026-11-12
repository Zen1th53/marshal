"""README links resolve, its anchors exist, and its setup prompt is docs/SETUP-PROMPT.md's."""
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
START, END = "<!-- setup-prompt:start -->\n", "\n<!-- setup-prompt:end -->"


def prompt_block(path):
    text = (ROOT / path).read_text(encoding="utf-8")
    if text.count(START) != 1 or text.count(END) != 1:
        raise AssertionError(f"{path}: setup-prompt markers must occur exactly once")
    return text.split(START, 1)[1].split(END, 1)[0]


def github_slug(heading):
    # GitHub: lower-case, drop punctuation except hyphens and spaces, spaces to hyphens.
    slug = re.sub(r"[^\w\- ]", "", heading.strip().lower())
    return slug.replace(" ", "-")


class ReadmeTests(unittest.TestCase):
    def test_setup_prompt_matches_docs(self):
        self.assertEqual(prompt_block("README.md"), prompt_block("docs/SETUP-PROMPT.md"))

    def test_setup_prompt_installs_with_the_published_line(self):
        self.assertIn(
            "curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh",
            prompt_block("README.md"),
        )

    def test_relative_links_and_anchors(self):
        text = (ROOT / "README.md").read_text(encoding="utf-8")
        # Fenced blocks are not links.
        prose = re.sub(r"(?ms)^(`{3,})[^\n]*\n.*?^\1\s*$", "", text)
        anchors = {github_slug(h) for h in re.findall(r"(?m)^#{1,6} (.+)$", prose)}
        targets = re.findall(r"\]\(([^)\s]+)\)", prose) + re.findall(r'src="([^"]+)"', prose)
        for target in targets:
            if re.match(r"[a-z]+://", target) or target.startswith("mailto:"):
                continue
            path, _, anchor = target.partition("#")
            if path:
                self.assertTrue((ROOT / path).exists(), f"README links to missing {path}")
            elif anchor:
                self.assertIn(anchor, anchors, f"README links to missing anchor #{anchor}")


if __name__ == "__main__":
    unittest.main()
