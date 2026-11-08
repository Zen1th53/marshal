//go:build !governed

package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// ProjectContextWatchSnapshot is observation data for an already authenticated
// runtime invocation. It cannot authorize a provider, resume, or completion.
type ProjectContextWatchSnapshot struct {
	Receipt   projectcontext.Receipt
	Snapshot  ProjectContextSnapshot
	Authority ProjectContextAuthorityObservation
}

// ReadProjectContextWatchSnapshot observes the immutable invocation receipt and
// current applicable context/authority in one SQLite snapshot. No global
// inventory digest or live worktree hash is compared: unrelated accepted
// context may translate, and authorized provider work changes its own tree.
func (s *Store) ReadProjectContextWatchSnapshot(ctx context.Context, a ProjectContextAdmission, receiptID string, now time.Time) (ProjectContextWatchSnapshot, error) {
	var result ProjectContextWatchSnapshot
	if now.IsZero() || receiptID == "" || a.ID != receiptID || a.ExpectedAuthority == nil || a.ExpectedStateDigest == "" || a.Binding.RequiredStateDigest != a.ExpectedStateDigest || a.Audience.Actor != a.Binding.Principal || a.Selection.TaskID != a.Binding.TaskID || a.Selection.CanonicalTaskID != a.Binding.CanonicalTaskID || normalizeProjectAuthorityBinding(a.ExpectedAuthority.Request.Binding) != normalizeProjectAuthorityBinding(a.Binding) {
		return result, projectcontext.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	result.Receipt, err = readProjectContextReceiptTx(ctx, tx, receiptID)
	if err != nil {
		return result, err
	}
	r := result.Receipt
	if r.Outcome != "QUALIFIED" || r.Stage != a.Stage || r.ParentReceiptID != a.ParentReceiptID || r.Binding != a.Binding || r.PacketDigest != a.ExpectedPacketDigest || (r.ValidUntil != nil && !now.Before(*r.ValidUntil)) || now.Before(r.CheckedAt) {
		return result, projectcontext.Changed("project_context_watch_extreme.go:41")
	}
	result.Authority, err = readProjectContextAuthorityTx(ctx, tx, a.ExpectedAuthority.Request, now)
	if err != nil {
		return result, err
	}
	if result.Authority.Digest != a.ExpectedAuthority.Digest {
		return result, projectcontext.Changed("project_context_watch_extreme.go:48")
	}
	result.Snapshot, err = readProjectContextSnapshotBound(ctx, tx, a.Binding.Project, a.Audience, a.Selection, now, a.Binding.Contract)
	if err != nil {
		return result, err
	}
	if result.Snapshot.StateDigest != a.ExpectedStateDigest {
		return result, projectcontext.Changed("project_context_watch_extreme.go:55")
	}
	return result, tx.Commit()
}
