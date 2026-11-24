package driver

import (
	"bytes"
	"encoding/json"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// maxTerminalRecord bounds the final record of a worker's stream, which is
// kept apart from the bounded head so a long stream cannot evict it.
const maxTerminalRecord = 1 << 20

// streamBuffer keeps the first limit bytes of a worker's stream, as
// capBuffer does, and separately the most recent tail of it, so the stream's
// final record survives a stream far longer than the head it keeps.
type streamBuffer struct {
	head    capBuffer
	tail    []byte
	written int64
}

func newStreamBuffer(limit int) *streamBuffer { return &streamBuffer{head: capBuffer{limit: limit}} }

func (b *streamBuffer) Write(p []byte) (int, error) {
	_, _ = b.head.Write(p)
	b.written += int64(len(p))
	b.tail = append(b.tail, p...)
	if len(b.tail) > 2*(maxTerminalRecord+1) {
		b.tail = append([]byte(nil), b.tail[len(b.tail)-(maxTerminalRecord+1):]...)
	}
	return len(p), nil
}

// String is the kept head with its truncation stated, for the output record.
func (b *streamBuffer) String() string { return b.head.String() }

// terminal returns the stream's final complete record, or false when it is
// longer than maxTerminalRecord.
// It also reports whether the record lies wholly inside the kept head.
func (b *streamBuffer) terminal() (record []byte, inHead, ok bool) {
	t := bytes.TrimRight(b.tail, "\r\n\t ")
	end := b.written - int64(len(b.tail)-len(t))
	i := bytes.LastIndexByte(t, '\n')
	if i < 0 && b.written > int64(len(b.tail)) {
		return nil, false, false
	}
	record = t[i+1:]
	if len(record) > maxTerminalRecord {
		return nil, false, false
	}
	return record, end <= int64(b.head.buf.Len()), true
}

// result parses the worker-reported actions and states any limit reached. A
// stream within its cap is parsed whole. Past the cap, the kept head and the
// separately retained final record are parsed, and the hand-in records that
// the stream was limited; a final record that was too long or is not JSON
// makes the hand-in's worker account incomplete, never a guessed one.
func (b *streamBuffer) result(parse func([]byte) []marshal.CommandRecord) ([]marshal.CommandRecord, []model.Limit) {
	if b.head.dropped == 0 {
		return parse(b.head.Bytes()), nil
	}
	limit := model.Limit{Kind: "worker_stream", Subject: "worker stream", ActualBytes: b.written, CapBytes: int64(b.head.limit), Limited: true,
		KeptBytes: int64(b.head.buf.Len()), Action: "The hand-in is judged on git and the re-run checks; the worker's account kept its first records and its final record."}
	stream := append([]byte(nil), b.head.Bytes()...)
	record, inHead, ok := b.terminal()
	if ok && json.Valid(record) {
		if !inHead {
			stream = append(append(stream, '\n'), record...)
			limit.KeptBytes += int64(len(record))
		}
	} else {
		limit.Kind = "worker_stream_incomplete"
		limit.Subject = "final record of the worker stream"
		limit.Action = "The worker's final record was too long or malformed, so its account is incomplete; the hand-in is judged on git and the re-run checks only. Inspect the worker's own log for its last message."
	}
	return parse(stream), []model.Limit{limit}
}
