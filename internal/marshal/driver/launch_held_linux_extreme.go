//go:build linux && !governed

package driver

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/Zen1th53/marshal/internal/processgroup"
)

// wrapHeld uses the private supervisor's startup barrier: the worker command
// cannot start until release writes its token.
func wrapHeld(cmd *exec.Cmd) (func() error, func(), error) {
	return processgroup.WrapHeld(cmd)
}

func heldIdentity(cmd *exec.Cmd) (ProcessIdentity, error) {
	boot, err := linuxBootID()
	if err != nil {
		return ProcessIdentity{}, err
	}
	ref, err := processgroup.Identify(cmd)
	if err != nil {
		return ProcessIdentity{}, err
	}
	if ref.PID <= 0 || ref.Start == "" {
		return ProcessIdentity{}, errors.New("held worker exited before its identity was recorded")
	}
	return ProcessIdentity{BootID: boot, PID: ref.PID, Start: ref.Start}, nil
}

func linuxBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("%w: boot identity unavailable: %v", ErrLaunchUnsupported, err)
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", fmt.Errorf("%w: boot identity empty", ErrLaunchUnsupported)
	}
	return id, nil
}

// HostProcesses observes this host's processes through /proc.
func HostProcesses() ProcessAdapter { return linuxProcesses{} }

type linuxProcesses struct{}

func (linuxProcesses) BootID() (string, error) { return linuxBootID() }

func (linuxProcesses) Find(key string) ([]ProcessIdentity, error) {
	boot, err := linuxBootID()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	want := []byte(LaunchKeyEnv + "=" + key)
	self := os.Getpid()
	var found []ProcessIdentity
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		env, err := os.ReadFile("/proc/" + e.Name() + "/environ")
		if err != nil {
			// A process that ended, or one owned by another user, cannot be
			// a worker this owner started.
			if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) || os.IsPermission(err) {
				continue
			}
			return nil, err
		}
		match := false
		for _, kv := range bytes.Split(env, []byte{0}) {
			if bytes.Equal(kv, want) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		ref, ok, err := processgroup.ReferenceOf(pid)
		if err != nil {
			return nil, err
		}
		if ok {
			found = append(found, ProcessIdentity{BootID: boot, PID: ref.PID, Start: ref.Start})
		}
	}
	return found, nil
}

func (linuxProcesses) Alive(p ProcessIdentity) (bool, error) {
	boot, err := linuxBootID()
	if err != nil {
		return false, err
	}
	if p.BootID != boot {
		return false, nil
	}
	return processgroup.RunningReference(processgroup.Reference{PID: p.PID, Start: p.Start})
}

func (linuxProcesses) Stop(p ProcessIdentity) error {
	boot, err := linuxBootID()
	if err != nil {
		return err
	}
	if p.BootID != boot {
		return nil
	}
	return processgroup.StopReference(processgroup.Reference{PID: p.PID, Start: p.Start})
}
