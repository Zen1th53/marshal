//go:build !governed && linux

package netpolicy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const agyOAuthWorkerRelativePath = ".gemini/antigravity-cli/antigravity-oauth-token"

func privateCredentialProfile(provider, model string) (string, string, string) {
	if provider == "agy" {
		return "GEMINI_API_KEY", "Authorization", "daily-cloudcode-pa.googleapis.com"
	}
	return "", "", ""
}
func privateNativeBroker(provider, model string) (*CredentialBroker, bool, error) {
	if provider != "agy" {
		return nil, false, nil
	}
	if _, err := loadAGYNativeCredential(context.Background()); err != nil {
		return nil, true, err
	}
	b, err := NewCredentialBroker("agy", model, "host-native-source-only")
	if err != nil {
		return nil, true, err
	}
	b.secret = ""
	b.nativeSource = &agyNativeSource{}
	b.hosts["www.googleapis.com"] = true
	b.idPlaceholder = agySyntheticID(b.scratchName, time.Now().Add(24*time.Hour))
	return b, true, nil
}

type agyNativeSource struct{}

func (*agyNativeSource) read(ctx context.Context, b *CredentialBroker) (nativeOAuthCredential, error) {
	c, err := loadAGYNativeCredential(ctx)
	if err != nil {
		return nativeOAuthCredential{}, err
	}
	replacements := map[string]string{c.access: b.placeholder, c.refresh: b.refreshPlaceholder}
	if c.id != "" { // the file layout carries no id token
		replacements[c.id] = b.idPlaceholder
	}
	return nativeOAuthCredential{access: c.access, expires: c.expires, replacements: replacements}, nil
}
func agySyntheticID(subject string, expiry time.Time) string {
	header, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": "https://accounts.google.com", "sub": subject, "email": "worker@invalid", "email_verified": false, "aud": "marshal-worker-placeholder", "iat": time.Now().Unix(), "exp": expiry.Unix()})
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".marshal-placeholder"
}
func (*agyNativeSource) prepare(home string, b *CredentialBroker) error {
	// Create only fresh synthetic native fallback state. Existing directories may
	// belong to the trusted honeypot, but symlinks and existing token files refuse.
	parent := home
	for _, name := range []string{".gemini", "antigravity-cli"} {
		parent = filepath.Join(parent, name)
		if err := os.Mkdir(parent, 0700); err != nil && !os.IsExist(err) {
			return ErrCredentialBroker
		}
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrCredentialBroker
		}
	}
	f, err := os.OpenFile(filepath.Join(home, agyOAuthWorkerRelativePath), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrCredentialBroker
	}
	defer f.Close()
	raw, err := json.Marshal(map[string]any{"token": map[string]any{"access_token": b.placeholder, "refresh_token": b.refreshPlaceholder, "token_type": "Bearer", "expiry": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}, "auth_method": "consumer", "id_token": b.idPlaceholder})
	if err != nil {
		return ErrCredentialBroker
	}
	if _, err = f.Write(raw); err != nil {
		return ErrCredentialBroker
	}
	return nil
}
func (*agyNativeSource) requestAllowed(req *http.Request, body []byte) bool {
	if req.URL.ForceQuery || req.URL.RawPath != "" {
		return false
	}
	// Pinned native CLI 1.2.14 requests SSE with this exact literal query.
	// Compare raw bytes so encoding, duplicates and extra parameters refuse.
	if req.URL.RawQuery != "" && (req.URL.RawQuery != "alt=sse" || req.Method != http.MethodPost || req.URL.Path != "/v1internal:streamGenerateContent" || (req.Host != "daily-cloudcode-pa.googleapis.com" && req.Host != "daily-cloudcode-pa.googleapis.com:443")) {
		return false
	}
	if agyNativeProfileRequest(req) {
		return req.Method == http.MethodGet && len(body) == 0
	}
	if (req.Host != "daily-cloudcode-pa.googleapis.com" && req.Host != "daily-cloudcode-pa.googleapis.com:443") || req.Method != http.MethodPost {
		return false
	}
	switch req.URL.Path {
	case "/v1internal:fetchUserInfo":
		return agyUserMetadataRequestAllowed(body)
	case "/v1internal:loadCodeAssist", "/v1internal:listExperiments", "/v1internal:fetchAvailableModels", "/v1internal:generateContent", "/v1internal:streamGenerateContent":
		return true
	}
	return false
}
func (*agyNativeSource) refusesAuthHost(host string) bool {
	switch normalizeHost(host) {
	case "oauth2.googleapis.com", "accounts.google.com":
		return true
	}
	return false
}
