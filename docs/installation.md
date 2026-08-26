# MARSHAL installation

MARSHAL v1.0.1 publishes Linux archives for amd64 and arm64. Bubblewrap is a
separately installed runtime dependency for sandboxed provider execution.

## Release archive

Download the archive for your architecture and `checksums.txt` from the
[v1.0.1 release](https://github.com/Zen1th53/marshal/releases/tag/v1.0.1).

```bash
sha256sum -c checksums.txt --ignore-missing
tar -xzf marshal_1.0.1_linux_amd64.tar.gz
install -Dm755 marshal "$HOME/.local/bin/marshal"
marshal version
```

The release also publishes an SPDX SBOM, build metadata, a release manifest,
and GitHub build-provenance attestations.

## Build from source

Go 1.25 or newer is required:

```bash
git clone https://github.com/Zen1th53/marshal.git
cd marshal
go test ./...
go build -o ./bin/marshal ./cmd/marshal
./bin/marshal version
```

For a release-style build with embedded metadata:

```bash
go build -trimpath \
  -ldflags="-X github.com/Zen1th53/marshal/internal/cli.Version=v1.0.1 \
  -X github.com/Zen1th53/marshal/internal/cli.Commit=$(git rev-parse HEAD) \
  -X github.com/Zen1th53/marshal/internal/cli.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o ./bin/marshal ./cmd/marshal
```

## Bubblewrap

Install `bwrap` with the host package manager:

```bash
# Debian / Ubuntu
sudo apt-get install bubblewrap

# Fedora
sudo dnf install bubblewrap

# Arch / BlackArch
sudo pacman -S bubblewrap
```

Verify the installation and initialize a Git repository for MARSHAL:

```bash
bwrap --version
cd /path/to/repository
marshal init
marshal doctor
```

Provider CLIs are optional and not bundled. Probe them explicitly with
`marshal doctor --probe-providers`.
