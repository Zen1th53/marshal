//go:build !governed && unix

package proposalmcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Live submission limits. A live submission is bounded twice: its inner
// proposal by the method's own cap, and the whole encoded request frame by
// MaxFrameBytes. The effective bound is whichever is reached first.
const (
	// MaxFrameBytes bounds one encoded request or response frame body,
	// without the socket's terminating newline.
	MaxFrameBytes = 16384
	// MaxProposalBytes bounds an ordinary proposal's inner payload.
	MaxProposalBytes = 4096
	// MaxContractProposalBytes bounds a project contract's inner payload.
	MaxContractProposalBytes = 64 << 10
	// MaxNoticeFrameBytes bounds a limit notice frame, envelope included.
	MaxNoticeFrameBytes = 1024
	// MaxNoticeFrame is the largest frame size a notice may report.
	MaxNoticeFrame = 16 << 20

	// NoticeMethod is the reserved method of a limit notice.
	NoticeMethod = "submission_limit_notice"

	LimitKindProposal = "live_proposal"
	LimitKindContract = "live_contract_proposal"
	LimitKindFrame    = "live_proposal_frame"
	LimitKindResponse = "live_proposal_response"

	// noticeTimeout bounds the whole notice delivery, retry included.
	noticeTimeout = 3 * time.Second
)

// Limit actions: the supported next step for each live submission cap.
const (
	ProposalLimitAction = "Send a shorter proposal: shorten its largest field to stay within 4096 bytes, or point at retained evidence instead of pasting it."
	ContractLimitAction = "Send a compact contract: shorten its largest fields (long summaries, rationale and descriptions) so the proposal stays within 65536 bytes."
	FrameLimitAction    = "Send a compact submission: the whole encoded request must stay within 16384 bytes, and quotes, backslashes, control characters, <, >, & and line separators take more bytes once escaped. Shorten the largest field and submit again."
)

// noticeMethods are the submission methods a limit notice may name.
var noticeMethods = map[string]bool{
	"submit": true, "project_contract_submit": true, "memory_submit": true,
	"design_review": true, "architecture_request_answer": true,
}

// NoticeMethodAllowed reports whether a notice may name method.
func NoticeMethodAllowed(method string) bool { return noticeMethods[method] }

// LimitNotice is the strict payload of a limit notice: the refused
// submission's method, its submission ID when valid or else an opaque
// correlation token, and the measured frame size. Nothing of the refused
// request's content is carried.
type LimitNotice struct {
	Method       string `json:"method"`
	SubmissionID string `json:"submission_id,omitempty"`
	Correlation  string `json:"correlation,omitempty"`
	FrameBytes   int64  `json:"frame_bytes"`
}

// ValidSubmissionID reports whether id is a submission ID a notice may
// carry: 1 to 64 ASCII letters, digits, '-' or '_'.
func ValidSubmissionID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// ValidCorrelation reports whether s is a correlation token: 32 lowercase
// hexadecimal characters.
func ValidCorrelation(s string) bool { return validSpoolID(s) }

// Validate checks a received notice strictly.
func (n LimitNotice) Validate() error {
	if !NoticeMethodAllowed(n.Method) {
		return errors.New("limit notice names no submission method")
	}
	if (n.SubmissionID == "") == (n.Correlation == "") {
		return errors.New("limit notice needs one submission_id or correlation")
	}
	if n.SubmissionID != "" && !ValidSubmissionID(n.SubmissionID) {
		return errors.New("limit notice submission_id is invalid")
	}
	if n.Correlation != "" && !ValidCorrelation(n.Correlation) {
		return errors.New("limit notice correlation is invalid")
	}
	if n.FrameBytes <= MaxFrameBytes || n.FrameBytes > MaxNoticeFrame {
		return errors.New("limit notice frame size is out of range")
	}
	return nil
}

// FrameLimit is the limit of a request frame of n bytes.
func FrameLimit(n int64) model.Limit {
	return model.Limit{Kind: LimitKindFrame, Subject: "request frame", ActualBytes: n, CapBytes: MaxFrameBytes, Limited: true, Action: FrameLimitAction}
}

// newLimitNotice is the notice for a refused request of method and args.
func newLimitNotice(method string, args json.RawMessage, frameBytes int) LimitNotice {
	n := LimitNotice{Method: method, FrameBytes: int64(frameBytes)}
	var id struct {
		SubmissionID string `json:"submission_id"`
	}
	if json.Unmarshal(args, &id) == nil && ValidSubmissionID(id.SubmissionID) {
		n.SubmissionID = id.SubmissionID
		return n
	}
	// A fixed-size digest identifies repeats of the same refused request
	// without carrying any of it.
	sum := sha256.Sum256(append([]byte(method+"\x00"), args...))
	n.Correlation = hex.EncodeToString(sum[:16])
	return n
}

// refuseFrame is the typed refusal of a request whose encoded frame is
// frameBytes long. It tells the broker in a compact notice first, when the
// method is a submission; the refusal says whether that notice arrived.
func refuseFrame(ctx context.Context, cfg Config, method string, args json.RawMessage, frameBytes int) error {
	limit := FrameLimit(int64(frameBytes))
	if field := largestField(args); field != "" {
		limit.Subject = "request frame (largest field: " + field + ")"
	}
	text := fmt.Sprintf("proposalmcp: the request is %d bytes once encoded, more than the %d-byte limit; it was not sent", frameBytes, MaxFrameBytes)
	if NoticeMethodAllowed(method) {
		if err := sendLimitNotice(ctx, cfg, newLimitNotice(method, args, frameBytes)); err != nil {
			text += "; the Board could not be told (notice delivery unconfirmed)"
		} else {
			text += "; the Board was told"
		}
	}
	return model.NewLimitError(errors.New(text), limit)
}

// sendLimitNotice delivers n to the broker on the same socket or spool path
// as any request, within noticeTimeout, retrying once with the same notice
// when the transport fails. A notice never triggers another notice.
func sendLimitNotice(ctx context.Context, cfg Config, n LimitNotice) error {
	if err := n.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	data, err := encodeRequest(cfg, NoticeMethod, raw)
	if err != nil {
		return err
	}
	if len(data) > MaxNoticeFrameBytes {
		return fmt.Errorf("proposalmcp: limit notice exceeds %d bytes", MaxNoticeFrameBytes)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), noticeTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		resp, err := send(ctx, cfg, data)
		if err == nil {
			_, err = decodeResponse(resp)
			return err
		}
		if attempt > 0 || ctx.Err() != nil {
			return err
		}
	}
}

// largestField names the largest top-level field of args, and of its
// proposal object when there is one, or "".
func largestField(args json.RawMessage) string {
	var top map[string]json.RawMessage
	if json.Unmarshal(args, &top) != nil {
		return ""
	}
	name, size := "", -1
	for k, v := range top {
		if len(v) > size {
			name, size = k, len(v)
		}
	}
	if name == "proposal" {
		var inner map[string]json.RawMessage
		if json.Unmarshal(top[name], &inner) == nil {
			best, bsize := "", -1
			for k, v := range inner {
				if len(v) > bsize {
					best, bsize = k, len(v)
				}
			}
			if best != "" {
				name = "proposal." + best
			}
		}
	}
	if len(name) > 80 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 }) {
		return ""
	}
	return name
}
