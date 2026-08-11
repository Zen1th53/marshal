
import tempfile
import unittest
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
import detached_sign

class DetachedSignTests(unittest.TestCase):
    def test_sign_verify_round_trip(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            priv = root/"private.pem"
            pub = root/"public.pem"
            msg = root/"msg.bin"
            sig = root/"msg.sig"
            msg.write_bytes(b"agent-os")
            detached_sign.generate_keypair(priv, pub)
            detached_sign.sign_file(priv, msg, sig)
            self.assertTrue(detached_sign.verify_file(pub, msg, sig))
            msg.write_bytes(b"tampered")
            self.assertFalse(detached_sign.verify_file(pub, msg, sig))

if __name__ == "__main__":
    unittest.main()
