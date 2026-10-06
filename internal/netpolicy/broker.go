package netpolicy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrCredentialBroker = errors.New("credential broker unavailable; governed provider work refused")

// CredentialBroker belongs to one egress run. Its CA key and provider key are
// memory-only host state. Prepare exports only a public certificate and an
// opaque, unissued credential. Profiles cannot be supplied by worker text.
type CredentialBroker struct {
	mu                                                         sync.Mutex
	provider, envKey, header, secret, placeholder, scratchName string
	refreshPlaceholder, idPlaceholder                          string
	hosts                                                      map[string]bool
	ca                                                         *x509.Certificate
	caKey                                                      *ecdsa.PrivateKey
	caPEM                                                      []byte
	upstreamRoots                                              *x509.CertPool
	certs                                                      map[string]tls.Certificate
	subscription                                               *subscriptionSource
	expiryAlerted                                              bool
}

func brokerProfile(provider, model string) (envKey, header, host string) {
	switch provider {
	case "codex":
		return "OPENAI_API_KEY", "Authorization", "api.openai.com"
	case "claude":
		return "ANTHROPIC_API_KEY", "X-Api-Key", "api.anthropic.com"
	case "gemini":
		return "GEMINI_API_KEY", "X-Goog-Api-Key", "generativelanguage.googleapis.com"
	case "opencode":
		switch strings.SplitN(model, "/", 2)[0] {
		case "openai":
			return "OPENAI_API_KEY", "Authorization", "api.openai.com"
		case "anthropic":
			return "ANTHROPIC_API_KEY", "X-Api-Key", "api.anthropic.com"
		case "google":
			return "GOOGLE_GENERATIVE_AI_API_KEY", "X-Goog-Api-Key", "generativelanguage.googleapis.com"
		case "deepseek":
			return "DEEPSEEK_API_KEY", "Authorization", "api.deepseek.com"
		}
	}
	return "", "", ""
}

// LoadCredentialBroker is called only after project operator consent. No host
// auth file is ever copied or written. Codex refresh uses a fixed host CLI
// protocol; Claude only rechecks sign-in maintained by the native host session.
func LoadCredentialBroker(provider, model string) (*CredentialBroker, error) {
	if provider == "opencode" && model == "" {
		model = os.Getenv("MARSHAL_OPENCODE_MODEL")
	}
	key, _, _ := brokerProfile(provider, model)
	secret := os.Getenv(key)
	if provider == "gemini" && secret == "" {
		secret = os.Getenv("GOOGLE_API_KEY")
	}
	if (provider == "codex" || provider == "claude") && secret == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, ErrCredentialBroker
		}
		dir, file := os.Getenv("CODEX_HOME"), "auth.json"
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		if provider == "claude" {
			dir = os.Getenv("CLAUDE_CONFIG_DIR")
			file = ".credentials.json"
			if dir == "" {
				dir = filepath.Join(home, ".claude")
			}
		}
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err == nil {
			var auth map[string]json.RawMessage
			if json.Unmarshal(raw, &auth) != nil {
				return nil, ErrCredentialBroker
			}
			if provider == "codex" {
				if tokens, ok := auth["tokens"]; ok && string(tokens) != "null" {
					return loadCodexSubscription(filepath.Join(dir, file))
				}
				_ = json.Unmarshal(auth["OPENAI_API_KEY"], &secret)
			} else if tokens, ok := auth["claudeAiOauth"]; ok && string(tokens) != "null" {
				return loadClaudeSubscription(filepath.Join(dir, file))
			}
		} else if !os.IsNotExist(err) {
			return nil, ErrCredentialBroker
		}
	}
	return NewCredentialBroker(provider, model, secret)
}

func NewCredentialBroker(provider, model, secret string) (*CredentialBroker, error) {
	env, header, host := brokerProfile(provider, model)
	if host == "" || secret == "" || strings.ContainsAny(secret, "\r\n\x00") {
		return nil, ErrCredentialBroker
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, ErrCredentialBroker
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, ErrCredentialBroker
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "MARSHAL run credential broker"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return nil, ErrCredentialBroker
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, ErrCredentialBroker
	}
	random := make([]byte, 24)
	if _, err = rand.Read(random); err != nil {
		return nil, ErrCredentialBroker
	}
	return &CredentialBroker{provider: provider, envKey: env, header: header, secret: secret, placeholder: "marshal-placeholder-" + hex.EncodeToString(random), scratchName: ".marshal-broker-" + hex.EncodeToString(random), refreshPlaceholder: "marshal-placeholder-refresh-" + hex.EncodeToString(random), idPlaceholder: placeholderJWT(".marshal-broker-"+hex.EncodeToString(random), time.Now().Add(25*time.Hour)), hosts: map[string]bool{host: true}, ca: ca, caKey: key, caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), certs: map[string]tls.Certificate{}}, nil
}

func (b *CredentialBroker) handles(host string) bool {
	return b != nil && b.hosts[normalizeHost(host)]
}

func (b *CredentialBroker) certificate(host string) (tls.Certificate, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cert, ok := b.certs[host]; ok {
		return cert, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, ErrCredentialBroker
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, ErrCredentialBroker
	}
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, NotBefore: b.ca.NotBefore, NotAfter: b.ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		leaf.IPAddresses = []net.IP{ip}
	} else {
		leaf.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, b.ca, &key.PublicKey, b.caKey)
	if err != nil {
		return tls.Certificate{}, ErrCredentialBroker
	}
	cert := tls.Certificate{Certificate: [][]byte{der, b.ca.Raw}, PrivateKey: key}
	b.certs[host] = cert
	return cert, nil
}

// Prepare uses a fresh, reserved directory in scratch HOME. The directory is
// outside provider tmpfs mounts. Existing entries (including symlinks) fail
// closed, and no host-native settings or auth metadata enter the scratch tree.
func (b *CredentialBroker) Prepare(home, sandboxHome string, roots []byte) ([]string, error) {
	pool := x509.NewCertPool()
	if !filepath.IsAbs(home) || !filepath.IsAbs(sandboxHome) || !pool.AppendCertsFromPEM(roots) {
		return nil, ErrCredentialBroker
	}
	dir := filepath.Join(home, b.scratchName)
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, ErrCredentialBroker
	}
	public := filepath.Join(dir, "ca.pem")
	bundle := filepath.Join(dir, "roots.pem")
	if os.WriteFile(public, b.caPEM, 0600) != nil || os.WriteFile(bundle, append(append([]byte(nil), roots...), b.caPEM...), 0600) != nil {
		return nil, ErrCredentialBroker
	}
	target := filepath.Join(sandboxHome, b.scratchName) + string(os.PathSeparator)
	env := []string{"SSL_CERT_FILE=" + target + "roots.pem", "NODE_EXTRA_CA_CERTS=" + target + "ca.pem", "CODEX_CA_CERTIFICATE=" + target + "ca.pem"}
	if b.subscription == nil {
		env = append(env, b.envKey+"="+b.placeholder)
	} else {
		// Clear the synthetic honeypot API key to select subscription auth.
		env = append(env, b.envKey+"=")
	}
	if b.provider == "gemini" {
		env = append(env, "GOOGLE_API_KEY="+b.placeholder)
	}
	if b.provider == "codex" {
		authDir := filepath.Join(dir, "codex")
		if os.Mkdir(authDir, 0700) != nil {
			return nil, ErrCredentialBroker
		}
		auth := map[string]any{"auth_mode": "apikey", "OPENAI_API_KEY": b.placeholder}
		if b.subscription != nil {
			auth = b.subscriptionAuth()
		}
		raw, _ := json.Marshal(auth)
		if os.WriteFile(filepath.Join(authDir, "auth.json"), raw, 0600) != nil {
			return nil, ErrCredentialBroker
		}
		env = append(env, "CODEX_HOME="+target+"codex")
	}
	if b.provider == "claude" && b.subscription != nil {
		authDir := filepath.Join(dir, "claude")
		if os.Mkdir(authDir, 0700) != nil {
			return nil, ErrCredentialBroker
		}
		raw, _ := json.Marshal(b.claudeAuth())
		if os.WriteFile(filepath.Join(authDir, ".credentials.json"), raw, 0600) != nil {
			return nil, ErrCredentialBroker
		}
		env = append(env, "CLAUDE_CONFIG_DIR="+target+"claude")
	}
	return env, nil
}

func CredentialProfileSupported(provider, model string) bool {
	if provider == "opencode" && model == "" {
		model = os.Getenv("MARSHAL_OPENCODE_MODEL")
	}
	_, _, host := brokerProfile(provider, model)
	return host != ""
}
