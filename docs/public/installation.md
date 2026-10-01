# Installation

MARSHAL runs in a Git project. Linux release archives support amd64 and arm64.
Provider CLIs are installed separately; see [provider setup](providers.md).

## Release archive

Choose an explicitly versioned 0.0.5 candidate from
[GitHub releases](https://github.com/Zen1th53/marshal/releases).
Download the Linux archive matching your architecture and its `checksums.txt`.
Use the actual downloaded archive name in place of `ARCHIVE.tar.gz`:

```bash
sha256sum -c checksums.txt --ignore-missing
tar -xzf ARCHIVE.tar.gz
install -Dm755 marshal "$HOME/.local/bin/marshal"
marshal version
```

Ensure `$HOME/.local/bin` is on `PATH`. Check that `marshal version` reports the
release you selected. `/update install` follows the latest published release;
it does not select this candidate for you.

## Source build

Use Go **1.25.13 or newer** and Git. This documentation describes the integration
branch for 0.0.5:

```bash
git clone --branch release/v0.0.5-rc.1 https://github.com/Zen1th53/marshal.git
cd marshal
go build -o ./bin/marshal ./cmd/marshal
./bin/marshal version
```

An unversioned source build reports the compiled version defaults rather than
the candidate tag. Run `./bin/marshal` in place of
`marshal`, or install that binary on `PATH`.

## Sandboxed execution prerequisite

Install Bubblewrap with your distribution's package manager, for example:

```bash
sudo apt-get install bubblewrap
bwrap --version
```

Governed provider execution can be blocked when required isolation or network
policy cannot be enforced. Native provider sessions use the provider's own
permissions. Continue with [first run](getting-started.md).
