package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// Session is what a provider CLI wrote about one chat session in its own
// transcript: the model it last answered with, the reasoning effort that turn
// ran at, and how much of the context window the last turn filled. Every field
// is as the CLI wrote it; a field the CLI did not write stays zero.
type Session struct {
	Model  string
	Effort string
	// ContextUsed is the tokens the last turn held in context; 0 unknown.
	ContextUsed int
	// ContextWindow is the model's context window the CLI reported; 0 when
	// the CLI writes none (Claude Code does not).
	ContextWindow int
	// AsOf is when the transcript was last written.
	AsOf time.Time
}

// ReadSession reads the newest turn of the chat transcript at path, written
// by provider ("claude" or "codex"; any other provider writes no transcript
// Marshal reads). ok is false when the file is missing or names no turn.
func ReadSession(provider, path string) (s Session, ok bool) {
	if path == "" {
		return Session{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return Session{}, false
	}
	switch provider {
	case "claude":
		s, ok = readClaudeSession(path)
	case "codex":
		s, ok = readCodexSession(path)
	}
	if ok {
		s.AsOf = info.ModTime()
	}
	return s, ok
}

// readClaudeSession is the newest main-thread assistant turn of a Claude Code
// transcript: its model, its effort and the input it held (fresh, cache
// written and cache read tokens).
func readClaudeSession(path string) (Session, bool) {
	var out Session
	found := false
	_ = eachTailLine(path, func(line []byte) bool {
		if !bytes.Contains(line, []byte(`"assistant"`)) {
			return true
		}
		var rec struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Effort      any    `json:"effort"`
			Message     struct {
				Model string `json:"model"`
				Usage struct {
					Input       int `json:"input_tokens"`
					CacheCreate int `json:"cache_creation_input_tokens"`
					CacheRead   int `json:"cache_read_input_tokens"`
					Output      int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" || rec.IsSidechain || !plainID(rec.Message.Model) {
			return true
		}
		out.Model = rec.Message.Model
		if e, ok := rec.Effort.(string); ok && plainID(e) {
			out.Effort = e
		}
		u := rec.Message.Usage
		out.ContextUsed = u.Input + u.CacheCreate + u.CacheRead + u.Output
		found = true
		return false
	})
	return out, found
}

// readCodexSession is the newest turn_context (model and effort) and the
// newest token_count that carries figures (tokens in context and the model's
// context window) of a Codex rollout.
func readCodexSession(path string) (Session, bool) {
	var out Session
	haveTurn, haveTokens := false, false
	_ = eachTailLine(path, func(line []byte) bool {
		switch {
		case !haveTurn && bytes.Contains(line, []byte(`"turn_context"`)):
			var rec struct {
				Type    string `json:"type"`
				Payload struct {
					Model  string `json:"model"`
					Effort string `json:"effort"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &rec) == nil && rec.Type == "turn_context" && plainID(rec.Payload.Model) {
				out.Model = rec.Payload.Model
				if plainID(rec.Payload.Effort) {
					out.Effort = rec.Payload.Effort
				}
				haveTurn = true
			}
		case !haveTokens && bytes.Contains(line, []byte(`"model_context_window"`)):
			var rec struct {
				Payload struct {
					Type string `json:"type"`
					Info *struct {
						Last struct {
							Total int `json:"total_tokens"`
						} `json:"last_token_usage"`
						Window int `json:"model_context_window"`
					} `json:"info"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &rec) == nil && rec.Payload.Type == "token_count" && rec.Payload.Info != nil {
				out.ContextUsed = max(0, rec.Payload.Info.Last.Total)
				out.ContextWindow = max(0, rec.Payload.Info.Window)
				haveTokens = true
			}
		}
		return !(haveTurn && haveTokens)
	})
	return out, haveTurn || haveTokens
}

// plainID is a short identifier a CLI writes for a model or an effort.
func plainID(s string) bool {
	if s == "" || len(s) > 64 || s == "<synthetic>" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '[' || c == ']') {
			return false
		}
	}
	return true
}

// eachTailLine calls fn with each complete line in the last tailBytes of
// path, newest first, until fn returns false.
func eachTailLine(path string, fn func([]byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	off := int64(0)
	if info.Size() > tailBytes {
		off = info.Size() - tailBytes
	}
	buf := make([]byte, info.Size()-off)
	n, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	buf = buf[:n]
	if off > 0 {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return nil
		}
		buf = buf[i+1:]
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return nil
	}
	buf = buf[:end]
	for len(buf) > 0 {
		start := bytes.LastIndexByte(buf, '\n') + 1
		if line := bytes.TrimRight(buf[start:], "\r"); len(line) > 0 && !fn(line) {
			return nil
		}
		if start == 0 {
			break
		}
		buf = buf[:start-1]
	}
	return nil
}
