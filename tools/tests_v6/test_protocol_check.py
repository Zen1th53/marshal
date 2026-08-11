
import unittest
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
import protocol_check

class ProtocolCheckTests(unittest.TestCase):
    def test_a2a_major_minor_compatibility(self):
        self.assertTrue(protocol_check.a2a_compatible("1.0", "1.0"))
        self.assertFalse(protocol_check.a2a_compatible("1.0", "0.3"))

    def test_mcp_exact_version_pin(self):
        self.assertTrue(protocol_check.mcp_compatible("2026-07-28", "2026-07-28"))
        self.assertFalse(protocol_check.mcp_compatible("2026-07-28", "2025-11-25"))

if __name__ == "__main__":
    unittest.main()
