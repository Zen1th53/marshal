package app

// This file exposes, for Process 01 startup assessment, the same enforcement
// facts the runtime uses when it decides whether work may execute.
//
// They are deliberately the same source. If startup consulted a different
// notion of "is the sandbox available" than execution does, the control center
// could report Ready for something the runtime would then refuse — which is
// precisely the UI-versus-backend divergence Process 00 forbids.

// SandboxEnforcementAvailable reports whether a trusted isolation binary is
// present and safe to use.
//
// The check is the same one the runtime performs before executing work: the
// binary must exist, be a regular file, and not be group- or world-writable. A
// writable isolation binary is not isolation, because anything able to modify
// it can choose what the sandbox does.
func SandboxEnforcementAvailable() bool {
	_, err := trustedBwrapPath()
	return err == nil
}

// EgressEnforcementAvailable reports whether outbound access can be restricted
// to approved destinations.
//
// It reports the runtime's actual capability rather than an aspiration. When
// this is false, MARSHAL blocks network-dependent work instead of allowing
// unrestricted access, and startup reports the capability as absent rather
// than limited.
func EgressEnforcementAvailable() bool {
	var runtime *Runtime
	return runtime.egressEnforcementAvailable()
}
