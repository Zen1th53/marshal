package lanes

import (
	"encoding/base64"
	"strings"
)

// ProgramStatus is the state reported via OSC 7501 Program Status Protocol.
type ProgramStatus struct {
	State    string // idle, working, blocked, done, error, clear
	Kind     string // permission, question, auth (when blocked)
	App      string // e.g. claude, codex, omp
	Msg      string // human-readable status, decoded if base64
	Progress int    // 0..100 (-1 if unset)
	Raw      string
}

// ParseOSC7501 extracts and parses OSC 7501 escape sequences from terminal output.
// Sequences follow the format:
//
//	ESC ] 7501 ; key=value:key=value ST
//
// where ST is ESC \ or BEL (\x07).
// Keys include: state, kind, app, msg (base64-encoded UTF-8), title (base64), progress, id.
// If multiple OSC 7501 sequences are present, the last valid one is returned (latest status).
func ParseOSC7501(output string) (ProgramStatus, bool) {
	var latest ProgramStatus
	found := false

	// Search for \x1b]7501;
	search := "\x1b]7501;"
	idx := 0
	for {
		start := strings.Index(output[idx:], search)
		if start < 0 {
			break
		}
		pos := idx + start + len(search)
		// Find terminator: either \x1b\\ or \x07
		end := -1
		termLen := 0
		for i := pos; i < len(output); i++ {
			if output[i] == '\x07' {
				end = i
				termLen = 1
				break
			}
			if output[i] == '\x1b' && i+1 < len(output) && output[i+1] == '\\' {
				end = i
				termLen = 2
				break
			}
		}
		if end < 0 {
			break
		}
		payload := output[pos:end]
		idx = end + termLen

		ps, ok := parsePayload(payload)
		if ok {
			latest = ps
			found = true
		}
	}

	return latest, found
}

func parsePayload(payload string) (ProgramStatus, bool) {
	ps := ProgramStatus{Progress: -1, Raw: payload}
	pairs := strings.Split(payload, ":")
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		switch k {
		case "state":
			ps.State = strings.TrimSpace(v)
		case "kind":
			ps.Kind = strings.TrimSpace(v)
		case "app":
			ps.App = strings.TrimSpace(v)
		case "msg":
			msgVal := strings.TrimSpace(v)
			// msg is base64-encoded UTF-8 in OSC 7501 spec
			if decoded, err := base64.StdEncoding.DecodeString(msgVal); err == nil {
				ps.Msg = string(decoded)
			} else {
				ps.Msg = msgVal
			}
		case "progress":
			var p int
			for _, r := range v {
				if r >= '0' && r <= '9' {
					p = p*10 + int(r-'0')
				}
			}
			ps.Progress = p
		}
	}
	if ps.State == "" {
		return ProgramStatus{}, false
	}
	return ps, true
}
