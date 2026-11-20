package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/sandbox"
)

func TestWorkerPlatformReusesThePlatformClassifier(t *testing.T) {
	saved := supervisedArgv
	t.Cleanup(func() { supervisedArgv = saved })
	supervisedArgv = func([]string) ([]string, string, error) {
		return nil, "", errors.New("socket confinement requires Linux amd64")
	}
	ok, host, why := WorkerPlatform()
	if host == "" {
		t.Fatal("no host name")
	}
	if sandbox.HostExecutionPreferred() {
		if !ok || why != "" {
			t.Fatalf("host-execution build refused workers: %v %q", ok, why)
		}
		return
	}
	if ok || why == "" {
		t.Fatalf("a confinement refusal read as a platform that runs workers: %v %q", ok, why)
	}
	supervisedArgv = func([]string) ([]string, string, error) { return nil, "", errors.New("permission denied") }
	if ok, _, _ := WorkerPlatform(); !ok {
		t.Fatal("an unrelated error read as a platform limit")
	}
}

func TestIsSignInFailure(t *testing.T) {
	if !IsSignInFailure("Error: not logged in") || IsSignInFailure("exit code 1") {
		t.Fatal("sign-in markers misread")
	}
}

func TestPlatformName(t *testing.T) {
	if got := platformName("linux", "amd64"); got != "Linux amd64" {
		t.Fatal(got)
	}
	if got := platformName("darwin", "arm64"); got != "macOS arm64" {
		t.Fatal(got)
	}
}
