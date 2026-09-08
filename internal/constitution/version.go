// Package constitution implements MARSHAL Process 00: the versioned,
// machine-enforceable control layer that governs Processes 01-08.
//
// The core invariant of this package is that any qualified AI may think for
// MARSHAL, but no AI may redefine MARSHAL. Nothing in this package accepts
// model output as authority: AI-supplied structures reach the gate only as
// advisory input, and every authoritative outcome is produced by deterministic
// code here or by the canonical primitives this package composes
// (internal/policy, internal/risk, internal/authz, internal/capability,
// internal/gate, internal/evidence).
package constitution

import (
	"fmt"
	"strings"
)

// Version identifies the exact constitutional semantics a session is bound to.
// A session's version is fixed at creation; it never changes mid-session.
// See Articles XX and XXII.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

// Current is the constitution version implemented by this build.
var Current = Version{Major: 1, Minor: 0, Patch: 0}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v Version) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0
}

// Compare orders versions. It returns a negative number when v precedes other,
// zero when they are identical, and a positive number when v follows other.
func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		return v.Major - other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor - other.Minor
	}
	return v.Patch - other.Patch
}

// CompatibleWith reports whether a session bound to v may be evaluated by a
// runtime implementing other. Major versions change constitutional meaning, so
// they must match exactly. A runtime may evaluate a session bound to an older
// minor/patch version of the same major line, but never a newer one: a newer
// binding implies semantics this build does not implement, and interpreting it
// under older rules would silently reinterpret the session (Article XXII).
func (v Version) CompatibleWith(other Version) bool {
	if v.Major != other.Major {
		return false
	}
	return v.Compare(other) <= 0
}

// ParseVersion parses a "major.minor.patch" string.
func ParseVersion(value string) (Version, error) {
	fields := strings.Split(strings.TrimSpace(value), ".")
	if len(fields) != 3 {
		return Version{}, fmt.Errorf("%w: constitution version %q must be major.minor.patch", ErrInvalidConstitution, value)
	}
	var parsed Version
	targets := []*int{&parsed.Major, &parsed.Minor, &parsed.Patch}
	for i, field := range fields {
		if field == "" {
			return Version{}, fmt.Errorf("%w: constitution version %q has an empty component", ErrInvalidConstitution, value)
		}
		n, err := parseNonNegative(field)
		if err != nil {
			return Version{}, fmt.Errorf("%w: constitution version %q: %v", ErrInvalidConstitution, value, err)
		}
		*targets[i] = n
	}
	return parsed, nil
}

func parseNonNegative(field string) (int, error) {
	n := 0
	for _, r := range field {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("component %q is not a number", field)
		}
		n = n*10 + int(r-'0')
		if n > 1<<20 {
			return 0, fmt.Errorf("component %q is out of range", field)
		}
	}
	return n, nil
}
