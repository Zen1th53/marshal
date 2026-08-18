# Installation

MARSHAL is supported on Linux. Release archives are produced for amd64 and
arm64. Bubblewrap (`bwrap`) is required for the strong worker sandbox; Git is
required for repository and worktree management.

## Release archive

```bash
version=1.0.0-rc.1
arch=amd64 # or arm64
curl -LO "https://github.com/Zen1th53/marshal/releases/download/v${version}/marshal_${version}_linux_${arch}.tar.gz"
curl -LO "https://github.com/Zen1th53/marshal/releases/download/v${version}/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "marshal_${version}_linux_${arch}.tar.gz"
install -Dm755 marshal "$HOME/.local/bin/marshal"
marshal version
```

The archive also contains the license, licensing summary, and installation
notes. Verify GitHub build provenance as described in
[Supply chain](security/supply-chain.md).

## Go install

Go 1.25 or newer is required.

```bash
go install github.com/Zen1th53/marshal/cmd/marshal@v1.0.0-rc.1
"$(go env GOPATH)/bin/marshal" version
```

## Runtime dependencies

- `git` for repository discovery, diffs, and isolated worktrees
- `bwrap` for sandboxed agent execution
- one supported provider CLI for provider-backed tasks
- an Ollama server only when using the OpenCode/Ollama path

Run `marshal doctor` inside the target Git repository. A missing sandbox or
provider is reported as unavailable; MARSHAL does not silently grant a weaker
security mode.

## From source

```bash
git clone https://github.com/Zen1th53/marshal.git
cd marshal
go build -o ./bin/marshal ./cmd/marshal
./bin/marshal version
go test ./...
```

Development builds report version `dev` unless release metadata is injected by
the release builder.
