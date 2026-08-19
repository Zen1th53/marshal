package mutation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrInvalidMutationSignature = errors.New("mutation signature verification failed")
	ErrInvalidRevisionChain     = errors.New("mutation previous revision does not match current head")
	ErrUnknownKeyEpoch          = errors.New("unknown key epoch for mutation envelope")
)

type MutationPayload struct {
	MemoryID       string `json:"memory_id"`
	PrevRevision   int    `json:"prev_revision"`
	NewRevision    int    `json:"new_revision"`
	ActorPrincipal string `json:"actor_principal"`
	Action         string `json:"action"`
	ContentDigest  string `json:"content_digest"`
}

type MutationEnvelope struct {
	MutationPayload
	Epoch     int       `json:"epoch"`
	Timestamp time.Time `json:"timestamp"`
	Signature string    `json:"signature"`
}

type Signer struct {
	mu     sync.RWMutex
	epoch  int
	secret []byte
}

func NewSigner(epoch int, secret []byte) *Signer {
	return &Signer{
		epoch:  epoch,
		secret: secret,
	}
}

func (s *Signer) RotateKey(newEpoch int, newSecret []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch = newEpoch
	s.secret = newSecret
}

func computeSignature(secret []byte, p MutationPayload, epoch int, t time.Time) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%s:%d:%d:%s:%s:%s:%d:%d", p.MemoryID, p.PrevRevision, p.NewRevision, p.ActorPrincipal, p.Action, p.ContentDigest, epoch, t.Unix())
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Signer) SignMutation(ctx context.Context, payload MutationPayload) (MutationEnvelope, error) {
	s.mu.RLock()
	epoch := s.epoch
	secret := s.secret
	s.mu.RUnlock()

	now := time.Now().UTC()
	sig := computeSignature(secret, payload, epoch, now)

	return MutationEnvelope{
		MutationPayload: payload,
		Epoch:           epoch,
		Timestamp:       now,
		Signature:       sig,
	}, nil
}

type Verifier struct {
	mu   sync.RWMutex
	keys map[int][]byte
}

func NewVerifier(keys map[int][]byte) *Verifier {
	kMap := make(map[int][]byte)
	for k, v := range keys {
		kMap[k] = v
	}
	return &Verifier{keys: kMap}
}

func (v *Verifier) AddEpochKey(epoch int, secret []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys[epoch] = secret
}

func (v *Verifier) VerifyMutation(ctx context.Context, env MutationEnvelope, currentHeadRev int) error {
	if env.PrevRevision != currentHeadRev {
		return fmt.Errorf("%w: expected prev rev %d, got %d", ErrInvalidRevisionChain, currentHeadRev, env.PrevRevision)
	}

	v.mu.RLock()
	secret, ok := v.keys[env.Epoch]
	v.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: epoch %d", ErrUnknownKeyEpoch, env.Epoch)
	}

	expectedSig := computeSignature(secret, env.MutationPayload, env.Epoch, env.Timestamp)
	if !hmac.Equal([]byte(expectedSig), []byte(env.Signature)) {
		return ErrInvalidMutationSignature
	}

	return nil
}
