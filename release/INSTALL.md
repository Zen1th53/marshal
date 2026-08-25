# Install MARSHAL

MARSHAL release binaries support Linux on amd64 and arm64.

1. Download the archive for your architecture and `checksums.txt` from the
   same GitHub release.
2. Verify the archive:

   ```bash
   sha256sum -c checksums.txt --ignore-missing
   ```

3. Extract and install the binary:

   ```bash
   tar -xzf marshal_1.0.1_linux_amd64.tar.gz
   install -Dm755 marshal "$HOME/.local/bin/marshal"
   marshal version
   ```

4. In an existing Git repository, initialize and diagnose MARSHAL:

   ```bash
   cd /path/to/repository
   marshal init
   marshal doctor
   ```

Bubblewrap is required for sandboxed provider execution on Linux. Provider
CLIs and credentials are optional and are not bundled. Use
`marshal doctor --probe-providers` to probe installed providers.
