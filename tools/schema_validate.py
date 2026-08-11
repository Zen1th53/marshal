
#!/usr/bin/env python3
from __future__ import annotations
import argparse, json
from pathlib import Path
from typing import Any

try:
    import jsonschema
except Exception:
    jsonschema = None

def _type_ok(value: Any, typ: str) -> bool:
    return {
        "object": isinstance(value, dict),
        "array": isinstance(value, list),
        "string": isinstance(value, str),
        "integer": isinstance(value, int) and not isinstance(value, bool),
        "number": isinstance(value, (int, float)) and not isinstance(value, bool),
        "boolean": isinstance(value, bool),
        "null": value is None,
    }.get(typ, True)

def _fallback_validate(instance: Any, schema: dict, path: str="$") -> list[str]:
    errors = []
    typ = schema.get("type")
    if isinstance(typ, str) and not _type_ok(instance, typ):
        return [f"{path}: expected {typ}"]
    if isinstance(instance, dict):
        for key in schema.get("required", []):
            if key not in instance:
                errors.append(f"{path}: missing required property {key}")
        props = schema.get("properties", {})
        for key, value in instance.items():
            if key in props and isinstance(props[key], dict):
                errors.extend(_fallback_validate(value, props[key], f"{path}.{key}"))
    if isinstance(instance, list) and isinstance(schema.get("items"), dict):
        for idx, value in enumerate(instance):
            errors.extend(_fallback_validate(value, schema["items"], f"{path}[{idx}]"))
    if "enum" in schema and instance not in schema["enum"]:
        errors.append(f"{path}: value not in enum")
    if "const" in schema and instance != schema["const"]:
        errors.append(f"{path}: value does not match const")
    return errors

def validate_instance(instance: Any, schema: dict) -> list[str]:
    if jsonschema is None:
        return _fallback_validate(instance, schema)
    try:
        validator_cls = jsonschema.validators.validator_for(schema)
        validator_cls.check_schema(schema)
        validator = validator_cls(schema)
        errs = sorted(validator.iter_errors(instance), key=lambda e: list(e.absolute_path))
        return [
            f"{'$.' + '.'.join(str(x) for x in e.absolute_path) if e.absolute_path else '$'}: {e.message}"
            for e in errs
        ]
    except Exception as exc:
        return [f"schema validation error: {exc}"]

def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("schema")
    p.add_argument("instance")
    args = p.parse_args()
    schema = json.loads(Path(args.schema).read_text())
    instance = json.loads(Path(args.instance).read_text())
    errors = validate_instance(instance, schema)
    print(json.dumps({"status":"PASS" if not errors else "FAIL","errors":errors}, indent=2))
    return 0 if not errors else 1

if __name__ == "__main__":
    raise SystemExit(main())
