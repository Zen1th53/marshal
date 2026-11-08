package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/model"
)

// EntitlementRequestTarget binds the exact cloud identity and previous durable
// generation. It never carries actor, role, capability, key material or lease.
type EntitlementRequestTarget struct {
	Key                cloud.RequestKey
	ExpectedGeneration int64
	WorkspaceSessionID string
}

// ULTRARequestService keeps the installation's private key out of TUI DTOs.
// It owns no lease/Gate and provides no polling or approval operation.
type ULTRARequestService struct {
	runtime          *Runtime
	client           *cloud.Client
	identity         cloud.State
	requests         *cloud.RequestStore
	key              cloud.RequestKey
	workspaceSession string
}

func NewULTRARequestService(r *Runtime, client *cloud.Client, identity cloud.State, workspaceSession, cloudSession string, requests *cloud.RequestStore) (*ULTRARequestService, error) {
	if r == nil || client == nil || requests == nil || workspaceSession == "" || cloudSession == "" || identity.InstallationID == "" {
		return nil, model.ErrInvalid
	}
	if _, err := identity.Public(); err != nil {
		return nil, err
	}
	identity.PrivateKey = append([]byte(nil), identity.PrivateKey...)
	return &ULTRARequestService{r, client, identity, requests, cloud.RequestKey{Endpoint: client.Endpoint(), InstallationID: identity.InstallationID, SessionID: cloudSession}, workspaceSession}, nil
}

func (s *ULTRARequestService) Read(ctx context.Context) (cloud.RequestObservation, bool, error) {
	if s == nil || s.requests == nil {
		return cloud.RequestObservation{}, false, model.ErrUnavailable
	}
	return s.requests.Read(ctx, s.key)
}

func (s *ULTRARequestService) Target(ctx context.Context) (EntitlementRequestTarget, error) {
	o, found, err := s.Read(ctx)
	if err != nil {
		return EntitlementRequestTarget{}, err
	}
	generation := int64(0)
	if found {
		generation = o.Generation
	}
	return EntitlementRequestTarget{Key: s.key, ExpectedGeneration: generation, WorkspaceSessionID: s.workspaceSession}, nil
}

func (s *ULTRARequestService) Submit(ctx context.Context, e CommandEnvelope, target EntitlementRequestTarget) (cloud.RequestObservation, error) {
	if s == nil || target.Key != s.key || target.WorkspaceSessionID != s.workspaceSession {
		return cloud.RequestObservation{}, model.ErrInvalid
	}
	return s.runtime.CommandRequestEntitlement(ctx, e, target, s.client, s.identity, s.requests)
}

// CommandRequestEntitlement uses the existing trusted local workspace context.
// Request submission is not a permission grant and consumes no operator
// capability to alter goals/memory/execution. Cloud PoP remains mandatory.
func (r *Runtime) CommandRequestEntitlement(ctx context.Context, e CommandEnvelope, target EntitlementRequestTarget, client *cloud.Client, identity cloud.State, requests *cloud.RequestStore) (cloud.RequestObservation, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || r.ProjectIdentity() != e.ProjectID {
		return cloud.RequestObservation{}, authz.ErrDenied
	}
	if client == nil || requests == nil || target.ExpectedGeneration < 0 || target.ExpectedGeneration != e.ExpectedVersion || target.WorkspaceSessionID != e.SessionID || target.Key.SessionID == "" || target.Key.InstallationID != identity.InstallationID || e.TargetID != model.SessionRecoveryDigest(target.Key) || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return cloud.RequestObservation{}, model.ErrInvalid
	}
	if client.Endpoint() != target.Key.Endpoint {
		return cloud.RequestObservation{}, model.ErrInvalid
	}
	b, _ := json.Marshal(struct {
		Envelope CommandEnvelope
		Target   EntitlementRequestTarget
	}{e, target})
	sum := sha256.Sum256(b)
	return client.RequestEntitlementTrackedCommand(ctx, identity, target.Key.SessionID, requests, e.ExpectedVersion, hex.EncodeToString(sum[:]))
}
