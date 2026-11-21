// Package buildversion carries the binary's version to the packages that
// report it on the wire (MCP serverInfo, the A2A agent card) without making
// them import the CLI. internal/cli sets Version at init from the same
// identity `marshal version` prints.
package buildversion

// Version is the running binary's version. It stays "dev" only when the CLI
// package is not linked, for example in a package's own unit tests.
var Version = "dev"
