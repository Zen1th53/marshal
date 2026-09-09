package cloud

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUnreachable marks a Cloud that could not be contacted. It is
	// deliberately distinct from a refusal: one means try again, the other
	// means the answer is no.
	ErrUnreachable = errors.New("cloud: unreachable")
	// ErrRefused marks a Cloud that answered and said no.
	ErrRefused = errors.New("cloud: refused")
	// ErrInsecureEndpoint marks an endpoint that is not HTTPS.
	ErrInsecureEndpoint = errors.New("cloud: endpoint must be https")
)

// requestTimeout bounds every Cloud call.
//
// ULTRA authorization sits in front of user-visible work, so a Cloud that has
// stopped responding must fail quickly to Standard rather than making MARSHAL
// feel hung. Ten seconds is long enough for a slow mobile link and short enough
// that a person does not wonder whether the tool has crashed.
const requestTimeout = 10 * time.Second

// retryInterval spaces out renewal attempts after a transient failure.
//
// It is a real wall-clock delay even when the gate's clock is injected, because
// its job is to be kind to a server that is already struggling, and a server
// does not care what clock the client is reasoning with.
const retryInterval = 15 * time.Second

// Client talks to the Community Cloud.
//
// It holds no authorization state of its own; that belongs to the Gate. This
// separation is what keeps "what the server said" and "what we are allowed to
// do" from drifting into the same mutable object.
type Client struct {
	endpoint string
	http     *http.Client
	version  string
}

// NewClient builds a Cloud client for an endpoint.
//
// Plain HTTP is refused rather than warned about. The lease is signed, so an
// eavesdropper cannot forge one, but they could observe an installation
// identifier over cleartext, and the telemetry rules exist precisely so that
// MARSHAL does not leak information about who is using it.
func NewClient(endpoint, clientVersion string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInsecureEndpoint, err)
	}
	// Loopback is allowed over HTTP so the server can be exercised locally
	// without a certificate. Nothing else is.
	if parsed.Scheme != "https" && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("%w: got %q", ErrInsecureEndpoint, parsed.Scheme)
	}
	return &Client{
		endpoint: strings.TrimRight(parsed.String(), "/"),
		version:  clientVersion,
		http: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				// TLS 1.2 is the floor. An impersonating endpoint that can only
				// negotiate something older is refused rather than trusted.
				TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
				MaxIdleConnsPerHost: 2,
			},
		},
	}, nil
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// post sends a JSON request and decodes a JSON response.
//
// It distinguishes three outcomes deliberately: transport failure, a refusal
// from the server, and success. Collapsing the first two would make an outage
// look like a revoked entitlement, and those call for opposite responses.
func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrRefused, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MARSHAL-Client-Version", c.version)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode >= 500:
		// A server error is the Cloud failing, not the client being refused,
		// so it degrades and retries rather than dropping the entitlement.
		return fmt.Errorf("%w: server returned %d", ErrUnreachable, resp.StatusCode)
	default:
		return fmt.Errorf("%w: server returned %d", ErrRefused, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: malformed response: %s", ErrRefused, err)
	}
	return nil
}

// Register enrolls this installation and returns the server's view of it.
func (c *Client) Register(ctx context.Context, st State) error {
	pub, err := st.Public()
	if err != nil {
		return err
	}
	return c.post(ctx, "/v1/installations/register", map[string]any{
		"installation_id": st.InstallationID,
		"public_key":      base64.RawURLEncoding.EncodeToString(pub),
		"client_version":  c.version,
	}, nil)
}

// challenge is the server's proof-of-possession challenge.
type challenge struct {
	Nonce     string    `json:"nonce"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// StartSession obtains a lease, proving possession of the installation key.
//
// The proof step is what makes a stolen lease worthless for obtaining the next
// one. Without it, anyone who captured a lease in transit could keep renewing
// it; with it, they would also need the private key, which never leaves the
// machine that generated it.
func (c *Client) StartSession(ctx context.Context, st State, sessionID string) (Lease, error) {
	var ch challenge
	if err := c.post(ctx, "/v1/ultra/challenge", map[string]any{
		"installation_id": st.InstallationID,
	}, &ch); err != nil {
		return Lease{}, err
	}
	if ch.Nonce == "" {
		return Lease{}, fmt.Errorf("%w: empty challenge", ErrRefused)
	}

	proof, err := st.Sign([]byte(st.InstallationID + "|" + ch.Nonce))
	if err != nil {
		return Lease{}, err
	}

	var lease Lease
	if err := c.post(ctx, "/v1/ultra/sessions", map[string]any{
		"installation_id": st.InstallationID,
		"session_id":      sessionID,
		"nonce":           ch.Nonce,
		"proof":           base64.RawURLEncoding.EncodeToString(proof),
		"client_version":  c.version,
	}, &lease); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

// RenewLease obtains a fresh lease for a session already in progress.
func (c *Client) RenewLease(ctx context.Context, st State, sessionID string) (Lease, error) {
	// Renewal repeats the proof rather than presenting the old lease. A lease
	// is a bearer token for its window; requiring the key again means capturing
	// one does not buy an indefinite extension.
	return c.StartSession(ctx, st, sessionID)
}

// Heartbeat reports that an ULTRA session is still live.
//
// A refusal here means the server has ended the session — revoked, expired or
// unknown — and the caller degrades. That is the path by which a revocation
// reaches a client that is already running.
func (c *Client) Heartbeat(ctx context.Context, st State, sessionID string) error {
	return c.post(ctx, "/v1/ultra/heartbeat", map[string]any{
		"installation_id": st.InstallationID,
		"session_id":      sessionID,
	}, nil)
}

// keyResponse carries the server's current verification keys.
type keyResponse struct {
	Keys []struct {
		KeyID     string `json:"key_id"`
		PublicKey string `json:"public_key"`
	} `json:"keys"`
}

// FetchKeys retrieves the signing keys this client should trust.
//
// Returning several is what makes rotation a non-event: the ring holds the
// outgoing and incoming key at once, so leases signed just before a rotation
// keep verifying until they expire on their own.
func (c *Client) FetchKeys(ctx context.Context) (*KeyRing, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/v1/keys", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: keys returned %d", ErrUnreachable, resp.StatusCode)
	}
	var body keyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: malformed keys: %s", ErrRefused, err)
	}
	ring := NewKeyRing()
	for _, k := range body.Keys {
		raw, err := base64.RawURLEncoding.DecodeString(k.PublicKey)
		if err != nil {
			// One malformed key does not invalidate the rest, but it is not
			// silently trusted either.
			continue
		}
		if err := ring.Add(k.KeyID, raw); err != nil {
			continue
		}
	}
	if ring.Len() == 0 {
		return nil, fmt.Errorf("%w: no usable verification keys", ErrRefused)
	}
	return ring, nil
}

// Session ties a Gate to a Client and keeps the lease fresh.
//
// It is the only thing in this package that runs on its own. Everything else is
// synchronous and testable without a clock.
type Session struct {
	client *Client
	gate   *Gate
	state  State
	id     string

	mu      sync.Mutex
	stopped bool
	stop    chan struct{}
}

// NewSession prepares a session. It does not contact the server.
func NewSession(client *Client, gate *Gate, state State, sessionID string) *Session {
	return &Session{
		client: client, gate: gate, state: state, id: sessionID,
		stop: make(chan struct{}),
	}
}

// Start obtains the first lease and installs it in the gate.
//
// An error here is not fatal to MARSHAL. It means ULTRA is unavailable and the
// session continues as Standard, which is the whole point of the Standard tier
// working offline.
func (s *Session) Start(ctx context.Context) error {
	lease, err := s.client.StartSession(ctx, s.state, s.id)
	if err != nil {
		return err
	}
	return s.gate.Adopt(lease)
}

// Maintain keeps the lease current until the context ends or Stop is called.
//
// Renewal failure degrades rather than retrying forever, and degrading is a
// mode change, not an error: the user keeps working, with Standard behaviour
// and confirmation prompts they would otherwise have delegated.
func (s *Session) Maintain(ctx context.Context) {
	for {
		renewAt, ok := s.gate.RenewAt()
		if !ok {
			return
		}
		// The delay is measured against the gate's clock, not the wall clock,
		// so a caller that injects a clock gets scheduling consistent with the
		// expiry decisions made from that same clock.
		wait := renewAt.Sub(s.gate.now())
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.gate.Degrade()
			return
		case <-s.stop:
			timer.Stop()
			s.gate.Degrade()
			return
		case <-timer.C:
		}

		lease, err := s.client.RenewLease(ctx, s.state, s.id)
		if err != nil {
			// A refusal is final: the entitlement is gone, so drop it now
			// rather than holding ULTRA until the current lease runs out.
			if errors.Is(err, ErrRefused) {
				s.gate.Degrade()
				return
			}
			// Unreachable is transient, so the existing lease is kept and
			// expiry is what eventually degrades. Retrying needs its own delay:
			// once renewal is due, RenewAt stays in the past, so looping
			// straight back would spin against a server that is already
			// struggling.
			select {
			case <-ctx.Done():
				s.gate.Degrade()
				return
			case <-s.stop:
				s.gate.Degrade()
				return
			case <-time.After(retryInterval):
			}
			continue
		}
		if err := s.gate.Adopt(lease); err != nil {
			s.gate.Degrade()
			return
		}
	}
}

// Stop ends maintenance and degrades to Standard.
func (s *Session) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	close(s.stop)
}
