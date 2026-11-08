//go:build !governed && linux

package netpolicy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/redaction"
)

// These fields are the pinned 1.2.14 native protobuf contract, not invented
// eligibility/account fields. The native server remains their authority.
func agyMetadataObject(raw []byte, limit int) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if checkAGYJSONValue(d, 0) != nil {
		return nil, false
	}
	if _, e := d.Token(); e != io.EOF {
		return nil, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}
func agyUserMetadataRequestAllowed(body []byte) bool {
	m, ok := agyMetadataObject(body, 4096)
	if !ok || !exactAGYJSONKeys(m, "project") {
		return false
	}
	var project string
	if json.Unmarshal(m["project"], &project) != nil || len(project) == 0 || len(project) > 512 {
		return false
	}
	for _, c := range project {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:/-", c)) {
			return false
		}
	}
	return true
}
func (*agyNativeSource) responseForWorker(req *http.Request, resp *http.Response, body []byte, secrets []string) ([]byte, bool) {
	if agyNativeProfileRequest(req) {
		return agySyntheticProfileResponse(resp, body, secrets)
	}
	return body, agyUserMetadataResponseAllowed(req, body, secrets) && agyGenerationResponseAllowed(req, resp, body, secrets)
}
func agyUserMetadataResponseAllowed(req *http.Request, body []byte, secrets []string) bool {
	if req.URL.Path != "/v1internal:fetchUserInfo" {
		return true
	}
	m, ok := agyMetadataObject(body, 64<<10)
	if !ok {
		return false
	}
	// Reject actual host credential values even when JSON escapes their bytes.
	var tree any
	if json.Unmarshal(body, &tree) != nil || !agyMetadataHasNoCredentials(tree, secrets) {
		return false
	}
	for key, raw := range m {
		switch key {
		case "userSettings":
			fields, ok := agyMetadataObject(raw, 4096)
			if !ok {
				return false
			}
			for key, raw := range fields {
				switch key {
				case "telemetryEnabled", "userDataCollectionForceDisabled", "marketingEmailsEnabled":
				default:
					return false
				}
				var v bool
				if string(raw) == "null" || json.Unmarshal(raw, &v) != nil {
					return false
				}
			}
		case "userTags":
			var tags []string
			if string(raw) == "null" || json.Unmarshal(raw, &tags) != nil || len(tags) > 128 {
				return false
			}
			for _, tag := range tags {
				if !agyMetadataText(tag, 256) {
					return false
				}
			}
		case "regionCode":
			var region string
			if string(raw) == "null" || json.Unmarshal(raw, &region) != nil || !agyMetadataText(region, 64) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func agyMetadataText(value string, limit int) bool {
	if len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
func agyMetadataHasNoCredentials(value any, secrets []string) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, value := range v {
			switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
			case "accesstoken", "refreshtoken", "idtoken", "authorization", "oauthauthorization":
				return false
			}
			if !agyMetadataHasNoCredentials(value, secrets) {
				return false
			}
		}
	case []any:
		for _, value := range v {
			if !agyMetadataHasNoCredentials(value, secrets) {
				return false
			}
		}
	case string:
		if containsBrokerSecret(v, secrets) || redaction.DetectCredentialShape(v) != "" {
			return false
		}
	}
	return true
}

// Only this fixed readonly metadata route is intercepted on the second host.
func agyNativeProfileRequest(req *http.Request) bool {
	return (req.Host == "www.googleapis.com" || req.Host == "www.googleapis.com:443") && req.URL.Path == "/oauth2/v2/userinfo"
}

// Google's documented OAuth2 v2 Userinfo fields are validated in host memory.
// Their actual identity is never passed to the worker or used as model authority.
// The pinned CLI needs only a local email/name display after a genuine 200.
func agySyntheticProfileResponse(resp *http.Response, body []byte, secrets []string) ([]byte, bool) {
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	m, ok := agyMetadataObject(body, 64<<10)
	if !ok {
		return nil, false
	}
	var tree any
	if json.Unmarshal(body, &tree) != nil || !agyMetadataHasNoCredentials(tree, secrets) {
		return nil, false
	}
	for key, raw := range m {
		switch key {
		case "email", "name", "id", "family_name", "given_name", "gender", "hd", "link", "locale", "picture":
			var value string
			if string(raw) == "null" || json.Unmarshal(raw, &value) != nil || !agyMetadataText(value, 4096) {
				return nil, false
			}
			if (key == "email" || key == "name") && (strings.TrimSpace(value) == "" || len(value) > 512) {
				return nil, false
			}
		case "verified_email":
			var value bool
			if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	if _, ok := m["email"]; !ok {
		return nil, false
	}
	if _, ok := m["name"]; !ok {
		return nil, false
	}
	// Do not emit verification, quota, region or eligibility assertions. Every
	// identity-bearing body field and upstream header/cookie is dropped.
	resp.Header = http.Header{"Content-Type": []string{"application/json"}}
	resp.Status = "200 OK"
	raw, _ := json.Marshal(map[string]string{"email": "worker@invalid", "name": "Marshal Worker"})
	return raw, true
}
