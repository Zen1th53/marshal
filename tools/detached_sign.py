
#!/usr/bin/env python3
from __future__ import annotations

import argparse
from pathlib import Path

try:
    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric.ed25519 import (
        Ed25519PrivateKey,
        Ed25519PublicKey,
    )
    from cryptography.exceptions import InvalidSignature
except Exception as exc:
    raise RuntimeError("cryptography package is required for detached Ed25519 signing") from exc


def generate_keypair(private_path: Path, public_path: Path) -> None:
    private_key = Ed25519PrivateKey.generate()
    public_key = private_key.public_key()

    private_path.write_bytes(
        private_key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.PKCS8,
            encryption_algorithm=serialization.NoEncryption(),
        )
    )
    public_path.write_bytes(
        public_key.public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
    )


def _load_private(path: Path) -> Ed25519PrivateKey:
    key = serialization.load_pem_private_key(path.read_bytes(), password=None)
    if not isinstance(key, Ed25519PrivateKey):
        raise TypeError("expected Ed25519 private key")
    return key


def _load_public(path: Path) -> Ed25519PublicKey:
    key = serialization.load_pem_public_key(path.read_bytes())
    if not isinstance(key, Ed25519PublicKey):
        raise TypeError("expected Ed25519 public key")
    return key


def sign_file(private_path: Path, data_path: Path, signature_path: Path) -> None:
    signature_path.write_bytes(_load_private(private_path).sign(data_path.read_bytes()))


def verify_file(public_path: Path, data_path: Path, signature_path: Path) -> bool:
    try:
        _load_public(public_path).verify(signature_path.read_bytes(), data_path.read_bytes())
        return True
    except InvalidSignature:
        return False


def main() -> int:
    p = argparse.ArgumentParser(description="Detached Ed25519 sign/verify helper")
    sub = p.add_subparsers(dest="cmd", required=True)

    g = sub.add_parser("generate-keypair")
    g.add_argument("--private", required=True)
    g.add_argument("--public", required=True)

    s = sub.add_parser("sign")
    s.add_argument("--private", required=True)
    s.add_argument("--file", required=True)
    s.add_argument("--signature", required=True)

    v = sub.add_parser("verify")
    v.add_argument("--public", required=True)
    v.add_argument("--file", required=True)
    v.add_argument("--signature", required=True)

    args = p.parse_args()
    if args.cmd == "generate-keypair":
        generate_keypair(Path(args.private), Path(args.public))
        return 0
    if args.cmd == "sign":
        sign_file(Path(args.private), Path(args.file), Path(args.signature))
        return 0
    ok = verify_file(Path(args.public), Path(args.file), Path(args.signature))
    print("PASS" if ok else "FAIL")
    return 0 if ok else 1

if __name__ == "__main__":
    raise SystemExit(main())
