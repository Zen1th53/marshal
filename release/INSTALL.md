# Install Marshal

Marshal release binaries support Linux and macOS on amd64 and arm64. On Windows, use WSL2.

The quickest route is the installer, which does all of the below and refuses to
install a download whose checksum does not match:

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

To do it by hand instead:

1. Download the archive for your architecture and `checksums.txt` from the
   same GitHub release.
2. Verify the archive:

   ```bash
   sha256sum -c checksums.txt --ignore-missing   # macOS: shasum -a 256 -c checksums.txt --ignore-missing
   ```

3. Extract and install the binary:

   ```bash
   tar -xzf marshal_<version>_<os>_<arch>.tar.gz   # e.g. linux_amd64, darwin_arm64
   install -Dm755 marshal "$HOME/.local/bin/marshal"
   marshal version
   ```

4. In an existing Git repository, initialize and diagnose Marshal:

   ```bash
   cd /path/to/repository
   marshal init
   marshal doctor
   ```

5. Launch a native provider session when its CLI is installed:

   ```bash
   marshal codex
   marshal claude
   marshal opencode
   ```

   Native OpenCode conversation and bounded tool evidence are automatically
   captured as project candidate memory when the OpenCode process exits.

Workers run on the host without a sandbox. Use a dedicated project directory;
`/` and `$HOME` itself are refused. The TUI requires tmux 3.3a or newer. Provider
CLIs and credentials are optional and are not bundled. Use
`marshal doctor --probe-providers` to probe installed providers.

Back up project state and stop old sessions before upgrading. Projects created
by v0.0.x keep their `.marshal/` state, and old schema-90 databases upgrade in
place. Recovery details are in
[the upgrade guide](https://github.com/Zen1th53/marshal/blob/main/docs/upgrading.md).

Anonymous telemetry is enabled at `https://marshal.blackhat.uz`. Opt out with
`marshal telemetry disable`, or set `MARSHAL_CLOUD_ENDPOINT=off` to prevent
all telemetry requests. Project content is never sent.
