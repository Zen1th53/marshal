# Install MARSHAL

MARSHAL release binaries support Linux on amd64 and arm64.

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
   sha256sum -c checksums.txt --ignore-missing
   ```

3. Extract and install the binary:

   ```bash
   tar -xzf marshal_<version>_linux_amd64.tar.gz
   install -Dm755 marshal "$HOME/.local/bin/marshal"
   marshal version
   ```

4. In an existing Git repository, initialize and diagnose MARSHAL:

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

Bubblewrap is required for sandboxed provider execution on Linux. Provider
CLIs and credentials are optional and are not bundled. Use
`marshal doctor --probe-providers` to probe installed providers.
