//go:build !governed

package app

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// A durable QUALIFIED receipt is inspectable data. This unexported live token
// belongs to one runtime and one invocation; no receipt/JSON/role can mint it.
// Callers hold their short owner/run fence during admission and consumption.
// No lock is retained across a model call. A post-commit policy change must
// cancel/quarantine ongoing work and is checked at the next effect boundary.
type requiredProjectInvocation struct {
	mu                sync.Mutex
	runtime           *Runtime
	principal         authz.Principal
	prepared          preparedRequiredProjectContext
	receipt           projectcontext.Receipt
	consumed          bool
	dispatchAdmission store.ProjectContextAdmission
}

func (r *Runtime) admitRequiredProjectInvocation(ctx context.Context, principal authz.Principal, b projectcontext.Binding, selection projectcontext.Selection, prefix string, byteCap int, stage, parent string) (*requiredProjectInvocation, error) {
	prepared, err := r.prepareRequiredProjectContext(ctx, principal, b, selection, prefix, byteCap)
	if err != nil {
		return nil, err
	}
	id, err := model.NewID("CTX-")
	if err != nil {
		return nil, err
	}
	prepared.Admission.ID, prepared.Admission.Stage, prepared.Admission.ParentReceiptID = id, stage, parent
	prepared.Admission.Now = time.Now().UTC()
	receipt, err := r.store.CommitProjectContextAdmission(ctx, prepared.Admission)
	if err != nil {
		return nil, err
	}
	// A SQL receipt alone cannot authorize dispatch. Re-observe external
	// worktree/authorization state while the caller still owns its run fence.
	current, err := r.prepareRequiredProjectContext(ctx, principal, prepared.Admission.Binding, selection, prefix, byteCap)
	if err != nil {
		return nil, err
	}
	if !sameRequiredInvocationPreparation(prepared, current) {
		return nil, projectcontext.Changed("project_context_invocation_extreme.go:54")
	}
	principal.Role.Authorities = append([]authz.Authority(nil), principal.Role.Authorities...)
	return &requiredProjectInvocation{runtime: r, principal: principal, prepared: prepared, receipt: receipt}, nil
}

// consumeRequiredProjectInvocation must be called immediately before the exact
// provider/native effect, using a current checked workspace observer and fence.
// A failure spends this volatile token and requires fresh admission. It never
// changes provider approvals or upgrades an old persisted coverage receipt.
func (r *Runtime) consumeRequiredProjectInvocation(ctx context.Context, token *requiredProjectInvocation) (string, projectcontext.Receipt, error) {
	if token == nil {
		return "", projectcontext.Receipt{}, projectcontext.ErrMissing
	}
	token.mu.Lock()
	defer token.mu.Unlock()
	if token.runtime != r || token.consumed {
		return "", projectcontext.Receipt{}, projectcontext.Changed("project_context_invocation_extreme.go:71")
	}
	// Spend before observations/SQL: an interrupted or refused effect cannot
	// turn this token into a later reusable invocation allowance.
	token.consumed = true
	if token.receipt.Outcome != "QUALIFIED" || (token.receipt.ValidUntil != nil && !time.Now().UTC().Before(*token.receipt.ValidUntil)) {
		return "", projectcontext.Receipt{}, projectcontext.Changed("project_context_invocation_extreme.go:77")
	}
	a := token.prepared.Admission
	current, err := r.prepareRequiredProjectContext(ctx, token.principal, a.Binding, a.Selection, a.Prefix, a.ByteCap)
	if err != nil {
		return "", projectcontext.Receipt{}, err
	}
	if !sameRequiredInvocationPreparation(token.prepared, current) {
		return "", projectcontext.Receipt{}, projectcontext.Changed("project_context_invocation_extreme.go:85")
	}
	stored, err := r.store.ReadProjectContextReceipt(ctx, token.receipt.ID)
	if err != nil || !reflect.DeepEqual(stored, token.receipt) {
		return "", projectcontext.Receipt{}, projectcontext.Changed("project_context_invocation_extreme.go:89")
	}
	id, err := model.NewID("CTX-INVOKE-")
	if err != nil {
		return "", projectcontext.Receipt{}, err
	}
	current.Admission.ID, current.Admission.Stage, current.Admission.ParentReceiptID = id, a.Stage, token.receipt.ID
	current.Admission.Now = time.Now().UTC()
	receipt, err := r.store.CommitProjectContextAdmission(ctx, current.Admission)
	if err != nil {
		return "", projectcontext.Receipt{}, err
	}
	if err = ctx.Err(); err != nil {
		return "", projectcontext.Receipt{}, err
	}
	if receipt.ValidUntil != nil && !time.Now().UTC().Before(*receipt.ValidUntil) {
		return "", projectcontext.Receipt{}, projectcontext.Changed("project_context_invocation_extreme.go:105")
	}
	// COMMIT linearizes canonical required-memory currentness. The owned run
	// fence independently pins external execution authority through this line.
	token.dispatchAdmission = current.Admission
	return current.Packet.Text, receipt, nil
}

func sameRequiredInvocationPreparation(a, b preparedRequiredProjectContext) bool {
	return a.Packet.Text == b.Packet.Text && a.Packet.Digest == b.Packet.Digest && a.Packet.ManifestDigest == b.Packet.ManifestDigest && a.Snapshot.StateDigest == b.Snapshot.StateDigest && a.Workspace == b.Workspace && a.Head == b.Head && a.Tree == b.Tree && a.TaskRevision == b.TaskRevision && a.AuthorityDigest == b.AuthorityDigest && a.Admission.Binding == b.Admission.Binding && reflect.DeepEqual(a.Admission.Selection, b.Admission.Selection)
}
