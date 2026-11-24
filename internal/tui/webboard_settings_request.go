package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/redaction"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

type webRequestBinding struct {
	Envelope app.CommandEnvelope
	Target   app.EntitlementRequestTarget
}

func webRequestDigest(b webRequestBinding) string {
	raw, _ := json.Marshal(b)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (w *Workspace) webRequestCapability(ctx context.Context) *webboard.SettingCapability {
	if editionExtreme || w.runtime == nil {
		return nil
	}
	service := w.runtime.WorkspaceEntitlementRequests()
	if service == nil {
		return nil
	}
	observation, found, err := service.Read(ctx)
	if err != nil || (found && observation.State != "refused" && observation.State != "denied") {
		return nil
	}
	target, err := service.Target(ctx)
	if err != nil {
		return nil
	}
	return &webboard.SettingCapability{Op: "settings/ultra.request", Target: webboard.Target{Kind: "entitlement-request", ID: model.SessionRecoveryDigest(target.Key)}, Revision: target.ExpectedGeneration}
}

// webSettingRequest begins on the input loop. Only journal admission is protected
// by the Web action lock; the tracked request runs after that lock is released.
func (w *Workspace) webSettingRequest(a webboard.Action) webboard.Result {
	if a.Command == nil || a.Commit == nil {
		return refused("Request authority unavailable.")
	}
	i := *a.Command
	var st *store.Store
	var service *app.ULTRARequestService
	var local *app.LocalControl
	var binding webRequestBinding
	res := a.Commit(func() webboard.Result {
		w.mu.RLock()
		defer w.mu.RUnlock()
		if editionExtreme || w.runtime == nil || w.store == nil || w.ultraProjectLocked() != i.ProjectID || webboard.SettingsEpoch(w.webSettingsFenceLocked()) != i.ProjectEpoch {
			return refused("The request target changed.")
		}
		st = w.store
		service = w.runtime.WorkspaceEntitlementRequests()
		if service == nil {
			return refused("Request service unavailable.")
		}
		target, err := service.Target(context.Background())
		if err != nil || target.ExpectedGeneration != i.Revision || model.SessionRecoveryDigest(target.Key) != i.Key {
			return refused("The request target changed.")
		}
		local, err = w.runtime.OpenLocalControl(context.Background())
		if err != nil {
			return refused("Request authority unavailable.")
		}
		binding = webRequestBinding{Envelope: app.CommandEnvelope{ProjectID: i.ProjectID, SessionID: target.WorkspaceSessionID, TargetID: i.Key, ExpectedVersion: i.Revision, IdempotencyKey: i.RequestID}, Target: target}
		raw, _ := json.Marshal(binding)
		if err = st.StartWebRequest(context.Background(), i, string(raw)); err != nil {
			return refused("The request journal could not be recorded.")
		}
		return webboard.Result{Status: "started", Text: "Entitlement request started; checking its durable outcome."}
	})
	if res.Status != "started" {
		return res
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = service.Submit(local.Context(ctx), binding.Envelope, binding.Target)
		// Read the durable request store even if the transport answer was lost.
		observation, found, err := service.ObserveCommand(context.Background(), binding.Envelope, binding.Target)
		r := webRequestReceipt(i, binding, observation, found, err)
		_ = st.FinishWebRequest(context.Background(), i, r)
		w.requestRepaint()
	}()
	return res
}
func webRequestReceipt(i store.WebIntent, b webRequestBinding, o cloud.RequestObservation, found bool, err error) store.WebReceipt {
	r := store.WebReceipt{RequestID: i.RequestID, Status: "interrupted", Code: "outcome_unknown", Revision: i.Revision, Effects: []string{"entitlement-request"}, Result: "Request outcome unknown; inspect entitlement request status before trying again."}
	if err != nil || !found || o.CommandDigest != webRequestDigest(b) {
		return r
	}
	r.Revision = o.Generation
	r.Result = "Local request " + o.LocalAttemptID + ": " + o.State + ". Execution still requires current entitlement."
	if o.ServerRequestID != "" {
		r.Result += " Server request: " + redaction.RedactAll(o.ServerRequestID) + "."
	}
	switch o.State {
	case "pending", "accepted", "active":
		r.Status = "applied"
		r.Code = o.State
	case "refused", "denied":
		r.Status = "refused"
		r.Code = "request_refused"
	}
	return r
}

// reconcileWebRequests only observes journals, including after authority expires.
func (w *Workspace) reconcileWebRequests(ctx context.Context) {
	w.reconcileWebWork(ctx)
	w.mu.RLock()
	st, runtime, project := w.store, w.runtime, w.ultraProjectLocked()
	w.mu.RUnlock()
	if st == nil || runtime == nil {
		return
	}
	service := runtime.WorkspaceEntitlementRequests()
	if service == nil {
		return
	}
	intents, err := st.UnfinishedWebRequests(ctx, project)
	if err != nil {
		return
	}
	for _, i := range intents {
		var binding webRequestBinding
		if json.Unmarshal([]byte(i.RequestBinding), &binding) != nil {
			continue
		}
		o, found, err := service.ObserveCommand(ctx, binding.Envelope, binding.Target)
		if !found || err != nil || o.State == "sending" {
			continue
		}
		_ = st.FinishWebRequest(ctx, i, webRequestReceipt(i, binding, o, found, err))
	}
}
