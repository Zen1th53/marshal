package tui

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func (w *Workspace) observeArchitectureOperatorInstruction(session string, index int, timestamp time.Time, text string) {
	if !architecturecodec.ModelArchitectureApprovalAllowed() || !store.ArchitectureAcceptanceInstruction(text) || w.runtime == nil || w.store == nil || session != w.boundMarshalArchitectureSource() {
		return
	}
	ref := fmt.Sprintf("operator:%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s", session, index, fmt.Sprint(timestamp), text))))
	// Each transcript message is observed exactly once per workspace: its
	// outcome (recorded or refused) is reported once, never on every poll,
	// and a refused message is never re-bound to a later draft.
	if _, seen := w.operatorInstructionsSeen.LoadOrStore(ref, struct{}{}); seen {
		return
	}
	ctx := w.workers.context()
	var message string
	fresh, err := w.runtime.RecordArchitectureOperatorInstruction(ctx, session, ref, text)
	switch {
	case err == nil:
		if !fresh {
			return
		}
		selection, _, observeErr := w.store.ObserveMarshalArchitecture(context.Background())
		if observeErr != nil {
			return
		}
		message = fmt.Sprintf("Recorded operator architecture instruction. instruction_ref=%s; architecture_id=%s; revision=%d; digest=%s. Submit accept-architecture with this exact binding.\n", ref, selection.ArchitectureID, selection.Revision, selection.Digest)
	case errors.Is(err, fs.ErrNotExist):
		// No architecture review exists: the Marshal is using the project
		// contract path. Bind the instruction to its pending contract draft.
		binding, contractFresh, contractErr := w.recordProjectContractInstruction(ctx, session, ref, text, timestamp)
		if contractErr != nil {
			w.RecordActivity("Architecture instruction refused: no architecture review and no bindable project contract draft: " + contractErr.Error())
			return
		}
		if !contractFresh {
			return
		}
		message = fmt.Sprintf("Recorded owner instruction for the project contract. instruction_ref=%s; contract_id=%s; revision=%d; digest=%s. Answer the pending project-contract Decision for exactly this contract with allow; if that Decision was already denied or expired, resubmit the identical contract with a fresh submission_id and allow its new Decision.\n", binding.Ref, binding.ID, binding.Revision, binding.Digest)
	default:
		w.RecordActivity("Architecture instruction refused: " + err.Error())
		return
	}
	view, err := openInboxView(w.runtime.ProjectRoot(), "marshal", false)
	if err == nil {
		err = view.write(message)
	}
	if err != nil {
		w.RecordActivity("Architecture instruction delivery failed: " + err.Error())
	}
	w.RecordActivity(message)
	// The owner answered: the Board's architecture gate closes.
	w.refreshGatesSoon()
}

func (w *Workspace) applyMarshalArchitectureAcceptance(p marshalProposal) {
	var err error = model.ErrPolicyDenied
	if architecturecodec.ModelArchitectureApprovalAllowed() && w.runtime != nil && p.who == "Marshal" && p.sourceSession != "" && p.sourceSession == w.boundMarshalArchitectureSource() && (strings.HasPrefix(p.occurrence, "runtime:") || strings.HasPrefix(p.occurrence, "mcp:")) {
		m := w.marshalSession()
		m.mu.Lock()
		review := m.architecture
		m.mu.Unlock()
		revision, parseErr := strconv.ParseInt(p.revision, 10, 64)
		if parseErr == nil {
			if review != nil {
				err = review.AcceptByMarshal(w.workers.context(), p.sourceSession, p.instructionRef, p.occurrence, p.id, revision, p.digest)
			} else {
				err = w.runtime.AcceptArchitectureByMarshal(w.workers.context(), p.sourceSession, p.instructionRef, p.occurrence, p.id, revision, p.digest)
			}
		}
	}
	w.resolveArchitectureProposal(p.occurrence, err)
	if err != nil {
		w.RecordActivity("Marshal architecture acceptance refused: " + err.Error())
		return
	}
	w.RecordActivity("Architecture accepted on recorded operator instruction: " + p.instructionRef)
}
