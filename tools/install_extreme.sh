#!/bin/sh
# Install an already reviewed Marshal artifact. No network or session control.
set -eu
artifact=
expected_sha=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --artifact) [ "$#" -ge 2 ] || exit 2; artifact=$2; shift 2 ;;
    --sha256) [ "$#" -ge 2 ] || exit 2; expected_sha=$2; shift 2 ;;
    *) echo 'Usage: install_extreme.sh --artifact PATH --sha256 REVIEWED_SHA256' >&2; exit 2 ;;
  esac
done
[ -n "$artifact" ] && [ "${#expected_sha}" -eq 64 ] || exit 2
case "$expected_sha" in *[!0-9a-f]*) exit 2 ;; esac
destination=${MARSHAL_BIN:-"$HOME/.local/bin/marshal"}
python3 - "$artifact" "$expected_sha" "$destination" <<'PY'
import datetime
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile

source, expected, output = sys.argv[1:]
source_info = os.lstat(source)
if not stat.S_ISREG(source_info.st_mode):
    raise SystemExit('Artifact must be a regular file without a symlink')
with open(source, 'rb') as stream:
    data = stream.read()
if hashlib.sha256(data).hexdigest() != expected:
    raise SystemExit('Artifact SHA-256 differs from the reviewed value')
target = Path(output).absolute()
target.parent.mkdir(parents=True, exist_ok=True)
if target.parent.resolve() != target.parent:
    raise SystemExit('Destination parent must have no symlink aliases')
if target.is_symlink() or (target.exists() and not target.is_file()):
    raise SystemExit('Destination must be absent or a regular file')
fd, candidate = tempfile.mkstemp(prefix='.marshal-install-', dir=target.parent)
try:
    with os.fdopen(fd, 'wb') as stream:
        os.fchmod(stream.fileno(), 0o755)
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())
    info = subprocess.run([candidate, '--json', 'version'], capture_output=True,
                          text=True, timeout=15, check=True,
                          env={**os.environ, "MARSHAL_CLOUD_ENDPOINT": "off"})
    version = json.loads(info.stdout)
    if version.get('edition') != 'Marshal':
        raise SystemExit('Artifact does not identify itself as a Marshal build')
    backup = None
    if target.exists():
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%d-%H%M%S')
        backup = str(target) + '.backup-' + stamp + '-' + str(os.getpid())
        with open(target, 'rb') as current, open(backup, 'xb') as retained:
            os.fchmod(retained.fileno(), stat.S_IMODE(target.stat().st_mode))
            while chunk := current.read(1024 * 1024):
                retained.write(chunk)
            retained.flush()
            os.fsync(retained.fileno())
    os.replace(candidate, target)
    directory = os.open(target.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)
    print(json.dumps({'installed': str(target), 'sha256': expected,
                      'version': version, 'backup': backup}))
finally:
    if os.path.exists(candidate):
        os.unlink(candidate)
PY
