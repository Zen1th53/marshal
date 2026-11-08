//go:build governed

package sandbox

// Community confines a worker's sockets to TCP through the run's egress
// proxy: no other socket family, and no connection but the proxy listener.
func buildSupervisorAllowsSocket(uint64) bool { return false }

func buildSupervisorAllowsConnect([]byte) bool { return false }

// Community refuses every sendmsg the filter traps and never reads worker
// memory to allow one.
func buildSupervisorAllowsMsgSend() bool { return false }

// Community sends every request, local names included, through the proxy.
func buildNoProxy() string { return "" }
