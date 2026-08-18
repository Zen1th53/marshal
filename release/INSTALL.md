# Install MARSHAL

MARSHAL release binaries support Linux on amd64 and arm64.

1. Verify the archive against `checksums.txt`:

   ```bash
   sha256sum -c checksums.txt
   ```

2. Extract the archive and install the binary:

   ```bash
   tar -xzf marshal_1.0.0-rc.1_linux_amd64.tar.gz
   install -Dm755 marshal "$HOME/.local/bin/marshal"
   marshal version
   marshal doctor
   ```

The archive also contains the open-source license and licensing guide. See the
repository documentation for sandbox requirements and the first-task workflow.
