package webcontrol

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrCodeInvalid     = errors.New("invalid or expired one-time login code")
	ErrCodeAlreadyUsed = errors.New("one-time login code has already been redeemed")
	ErrSessionNotFound = errors.New("session not found or expired")
	ErrSessionExpired  = errors.New("session has expired")
)

const (
	SessionCookieName  = "marshal_session"
	DefaultIdleTTL     = 30 * time.Minute
	DefaultAbsoluteTTL = 24 * time.Hour
	OneTimeCodeTTL     = 5 * time.Minute
)

type OneTimeCode struct {
	Code        string
	PrincipalID string
	Role        string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Redeemed    bool
}

type Session struct {
	ID             string
	PrincipalID    string
	Role           string
	CreatedAt      time.Time
	LastAccessedAt time.Time
	ExpiresAt      time.Time
}

type SessionStore struct {
	mu          sync.RWMutex
	otcMap      map[string]*OneTimeCode
	sessions    map[string]*Session
	idleTTL     time.Duration
	absoluteTTL time.Duration
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		otcMap:      make(map[string]*OneTimeCode),
		sessions:    make(map[string]*Session),
		idleTTL:     DefaultIdleTTL,
		absoluteTTL: DefaultAbsoluteTTL,
	}
}

func generateRandomToken(bytesLen int) (string, error) {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto rand failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func otcFilePath() string {
	if fi, err := os.Stat(".marshal"); err == nil && fi.IsDir() {
		return filepath.Join(".marshal", "otc.json")
	}
	return filepath.Join(os.TempDir(), "marshal_otc.json")
}

func (s *SessionStore) loadOTCsLocked() {
	p := otcFilePath()
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var list []*OneTimeCode
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	now := time.Now().UTC()
	for _, item := range list {
		if item != nil && !item.Redeemed && now.Before(item.ExpiresAt) {
			s.otcMap[item.Code] = item
		}
	}
}

func (s *SessionStore) saveOTCsLocked() {
	p := otcFilePath()
	now := time.Now().UTC()
	var list []*OneTimeCode
	for _, item := range s.otcMap {
		if item != nil && !item.Redeemed && now.Before(item.ExpiresAt) {
			list = append(list, item)
		}
	}
	data, err := json.Marshal(list)
	if err == nil {
		_ = os.WriteFile(p, data, 0o600)
	}
}

func (s *SessionStore) CreateOneTimeCode(principalID, role string) (string, error) {
	code, err := generateRandomToken(16)
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loadOTCsLocked()
	s.otcMap[code] = &OneTimeCode{
		Code:        code,
		PrincipalID: principalID,
		Role:        role,
		CreatedAt:   now,
		ExpiresAt:   now.Add(OneTimeCodeTTL),
		Redeemed:    false,
	}
	s.saveOTCsLocked()

	return code, nil
}

func (s *SessionStore) RedeemOneTimeCode(code string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loadOTCsLocked()
	otc, ok := s.otcMap[code]
	if !ok || otc == nil {
		return nil, ErrCodeInvalid
	}

	if otc.Redeemed {
		return nil, ErrCodeAlreadyUsed
	}

	now := time.Now().UTC()
	if now.After(otc.ExpiresAt) {
		delete(s.otcMap, code)
		s.saveOTCsLocked()
		return nil, ErrCodeInvalid
	}

	// Burn code immediately (single-use invariant)
	otc.Redeemed = true
	delete(s.otcMap, code)
	s.saveOTCsLocked()

	// Create new session ID (session fixation defense)
	sessionID, err := generateRandomToken(32)
	if err != nil {
		return nil, err
	}

	session := &Session{
		ID:             sessionID,
		PrincipalID:    otc.PrincipalID,
		Role:           otc.Role,
		CreatedAt:      now,
		LastAccessedAt: now,
		ExpiresAt:      now.Add(s.absoluteTTL),
	}

	s.sessions[sessionID] = session
	return session, nil
}

func (s *SessionStore) GetSession(sessionID string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok || session == nil {
		return nil, ErrSessionNotFound
	}

	now := time.Now().UTC()
	// Check absolute expiration
	if now.After(session.ExpiresAt) {
		delete(s.sessions, sessionID)
		return nil, ErrSessionExpired
	}

	// Check idle expiration
	if now.Sub(session.LastAccessedAt) > s.idleTTL {
		delete(s.sessions, sessionID)
		return nil, ErrSessionExpired
	}

	// Update activity
	session.LastAccessedAt = now
	return session, nil
}

func (s *SessionStore) RevokeSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}
