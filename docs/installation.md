# MARSHAL Installation Guide

MARSHAL is packaged as a static, single binary for Linux systems.

---

## Supported Operating Systems

- **Linux (x86_64 / amd64)**: Ubuntu 22.04+, Debian 11+, Fedora 38+, Arch Linux, BlackArch, Alpine Linux
- **Linux (arm64)**: Ubuntu 22.04+, Debian 11+, Raspberry Pi OS (64-bit)

---

## Method 1: Pre-Built Binary Release (Recommended)

Download the latest release archive from [GitHub Releases](https://github.com/Zen1th53/marshal/releases):

```bash
# Download latest release tarball for Linux amd64
curl -LO https://github.com/Zen1th53/marshal/releases/download/v1.0.0/marshal_1.0.0_linux_amd64.tar.gz

# Extract archive
tar -xzf marshal_1.0.0_linux_amd64.tar.gz

# Verify binary execution
./marshal version

# Install to system path
sudo install -m 0755 marshal /usr/local/bin/
```

### Checksum Verification
```bash
# Download checksums file
curl -LO https://github.com/Zen1th53/marshal/releases/download/v1.0.0/checksums.txt

# Verify SHA-256 hash
sha256sum --ignore-missing -c checksums.txt
```

---

## Method 2: Install via Go (`go install`)

Requires Go `1.25` or higher installed:

```bash
go install github.com/Zen1th53/marshal/cmd/marshal@latest
```

Verify installation:
```bash
marshal version
```

---

## Method 3: Build from Source

```bash
# Clone repository
git clone https://github.com/Zen1th53/marshal.git
cd marshal

# Run test suite
go test ./...

# Build production binary
go build -ldflags="-X 'github.com/Zen1th53/marshal/internal/cli.Version=v1.0.0' -X 'github.com/Zen1th53/marshal/internal/cli.Commit=$(git rev-parse --short HEAD)' -X 'github.com/Zen1th53/marshal/internal/cli.BuildDate=$(date -u +%Y-%m-%d)'" -o bin/marshal ./cmd/marshal

# Install
sudo install -m 0755 bin/marshal /usr/local/bin/
```

---

## System Dependencies

MARSHAL relies on Linux kernel namespaces for process sandboxing:

### Bubblewrap (`bwrap`)
Required for fail-closed sandbox execution cells.

- **Ubuntu / Debian**: `sudo apt-get install -y bubblewrap`
- **Fedora / RHEL**: `sudo dnf install -y bubblewrap`
- **Arch / BlackArch**: `sudo pacman -S bubblewrap`
- **Alpine**: `sudo apk add bubblewrap`

Verify bubblewrap installation:
```bash
bwrap --version
```
