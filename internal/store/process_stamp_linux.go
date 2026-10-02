//go:build linux

package store

import (
	"os"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

func supervisorProcessStamp(pid int) (string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	// comm is parenthesized and may contain spaces or closing parentheses.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", model.ErrInvalid
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return "", model.ErrInvalid
	}
	return fields[19], nil // /proc stat field 22: process start time, not PID alone.
}
