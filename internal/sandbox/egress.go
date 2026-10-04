package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
)

const SandboxProxyURL = "http://127.0.0.1:18080"
const sandboxEgressSocket = "/run/marshal-egress/proxy.sock"

// TrustedBridgePath deliberately ignores worker-controlled PATH entries.
func TrustedBridgePath() (string, error) {
	for _, path := range []string{"/usr/bin/socat", "/bin/socat"} {
		if err := validateBridge(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("trusted socat bridge is unavailable")
}

func validateBridge(path string) error {
	if path != "/usr/bin/socat" && path != "/bin/socat" {
		return fmt.Errorf("untrusted bridge path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("trusted socat bridge is unavailable")
	}
	return nil
}

func egressEnvelope(socket, bridge string, command []string) ([]string, []string, error) {
	if err := validateBridge(bridge); err != nil {
		return nil, nil, err
	}
	if !filepath.IsAbs(socket) {
		return nil, nil, fmt.Errorf("egress socket must be absolute")
	}
	info, err := os.Stat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, nil, fmt.Errorf("egress socket unavailable")
	}
	args := []string{"--dir", "/run/marshal-egress", "--ro-bind", socket, sandboxEgressSocket, "--ro-bind", bridge, bridge}
	// Positional parameters preserve the worker argv and stdin. Both the Unix
	// connection and local TCP listener must work before the worker starts.
	script := `bridge=$1; shift
"$bridge" -T 1 -u OPEN:/dev/null UNIX-CONNECT:/run/marshal-egress/proxy.sock </dev/null || exit 125
"$bridge" TCP4-LISTEN:18080,bind=127.0.0.1,reuseaddr,fork UNIX-CONNECT:/run/marshal-egress/proxy.sock </dev/null &
pid=$!
trap 'kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null' EXIT
tries=0
until "$bridge" -T 1 -u OPEN:/dev/null TCP4:127.0.0.1:18080 </dev/null 2>/dev/null; do
  kill -0 "$pid" 2>/dev/null || exit 125
  tries=$((tries+1)); [ "$tries" -lt 50 ] || exit 125
  /bin/sleep 0.02
done
"$@"
status=$?
exit "$status"`
	argv := append([]string{"/bin/sh", "-c", script, "marshal-egress", bridge}, command...)
	return args, argv, nil
}

func proxyEnvironment() []string {
	var env []string
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		env = append(env, key+"="+SandboxProxyURL)
	}
	return append(env, "NO_PROXY=", "no_proxy=")
}
