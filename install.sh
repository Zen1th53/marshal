#!/bin/sh
# MARSHAL installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
#
# Downloads the release binary for this machine, verifies it against the
# release's published checksums, and installs it. Nothing is run with sudo and
# nothing outside the install directory is touched.
#
# Environment:
#   MARSHAL_VERSION      release tag to install (default: the latest release)
#   MARSHAL_INSTALL_DIR  where to put the binary (default: ~/.local/bin)

set -eu

REPO="Zen1th53/marshal"
INSTALL_DIR="${MARSHAL_INSTALL_DIR:-$HOME/.local/bin}"

info() { printf '%s\n' "$*" >&2; }
die() { printf 'marshal: %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "this installer needs $1, which is not on PATH"
}

# Releases are built for Linux only. Saying so beats installing a binary that
# cannot run, or silently doing nothing.
os=$(uname -s)
[ "$os" = "Linux" ] || die "release binaries are built for Linux; this is $os.
  Build from source instead: go install github.com/$REPO/cmd/marshal@latest"

case $(uname -m) in
    x86_64 | amd64) arch="amd64" ;;
    aarch64 | arm64) arch="arm64" ;;
    *) die "no release binary for $(uname -m); MARSHAL publishes linux amd64 and arm64" ;;
esac

need curl
need tar
need install

# sha256sum is coreutils; shasum is the Perl one. Either verifies the download,
# and an installer that skipped verification when neither was present would be
# defeating its own checksum file.
if command -v sha256sum >/dev/null 2>&1; then
    checksum_cmd="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
    checksum_cmd="shasum -a 256"
else
    die "this installer needs sha256sum or shasum to verify the download"
fi

tag="${MARSHAL_VERSION:-}"
if [ -z "$tag" ]; then
    info "Resolving the latest MARSHAL release..."
    tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
        sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
        head -n 1)
    [ -n "$tag" ] || die "could not resolve the latest release; set MARSHAL_VERSION to a tag"
fi
version="${tag#v}"

archive="marshal_${version}_linux_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
# Leave nothing behind, including when the download or verification fails.
trap 'rm -rf "$tmp"' EXIT INT TERM

info "Downloading MARSHAL $tag for linux/$arch..."
curl -fsSL -o "$tmp/$archive" "$base/$archive" ||
    die "could not download $archive from release $tag"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" ||
    die "release $tag publishes no checksums.txt; refusing to install unverified"

info "Verifying checksum..."
(cd "$tmp" && grep " $archive\$" checksums.txt > expected.txt &&
    $checksum_cmd -c expected.txt >/dev/null 2>&1) ||
    die "checksum verification failed for $archive; the download was not installed"

tar -xzf "$tmp/$archive" -C "$tmp" ||
    die "could not extract $archive"
[ -f "$tmp/marshal" ] || die "$archive does not contain a marshal binary"

install -Dm755 "$tmp/marshal" "$INSTALL_DIR/marshal" ||
    die "could not install to $INSTALL_DIR; set MARSHAL_INSTALL_DIR to a writable directory"

info ""
info "MARSHAL $tag installed to $INSTALL_DIR/marshal"

# A binary the shell cannot find is not installed as far as the user is
# concerned, so say so rather than letting the next command fail.
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        info ""
        info "$INSTALL_DIR is not on your PATH. Add it:"
        info "  export PATH=\"$INSTALL_DIR:\$PATH\""
        ;;
esac

info ""
info "Next:"
info "  marshal version"
info "  cd /path/to/your/repository && marshal init && marshal doctor"
info "  marshal opencode   # native OpenCode; conversation memory saves on exit"
