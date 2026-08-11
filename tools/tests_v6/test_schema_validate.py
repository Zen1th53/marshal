
import json
import tempfile
import unittest
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
import schema_validate

class SchemaValidateTests(unittest.TestCase):
    def test_validates_required_fields(self):
        schema = {
            "type": "object",
            "required": ["id"],
            "properties": {"id": {"type": "string"}}
        }
        self.assertEqual(schema_validate.validate_instance({"id": "X"}, schema), [])
        errors = schema_validate.validate_instance({}, schema)
        self.assertTrue(any("id" in e for e in errors))

    def test_rejects_wrong_type(self):
        schema = {"type": "object", "properties": {"n": {"type": "integer"}}}
        errors = schema_validate.validate_instance({"n": "1"}, schema)
        self.assertTrue(errors)

if __name__ == "__main__":
    unittest.main()
