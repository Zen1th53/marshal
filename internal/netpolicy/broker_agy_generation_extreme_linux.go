//go:build !governed && linux

package netpolicy

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Validate native generation before exposing any bytes. Credential reflection
// is refused after JSON decoding, including arbitrary Unicode escapes in keys
// or values. Clean JSON and SSE framing are retained byte for byte.
func agyGenerationResponseAllowed(req *http.Request, resp *http.Response, body []byte, secrets []string) bool {
	if req.URL.Path != "/v1internal:generateContent" && req.URL.Path != "/v1internal:streamGenerateContent" {
		return true
	}
	variants := agyGenerationSecretVariants(secrets)
	if len(body) == 0 || len(body) > brokerBodyLimit || !utf8.Valid(body) || containsBrokerSecret(string(body), variants) {
		return false
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	if media != "text/event-stream" {
		return media == "application/json" && agyGenerationJSONHasNoVariants(body, variants)
	}
	if req.URL.Path != "/v1internal:streamGenerateContent" {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), brokerBodyLimit)
	var data []string
	events := 0
	flush := func() bool {
		if len(data) == 0 {
			return true
		}
		value := strings.Join(data, "\n")
		data = nil
		events++
		return value == "[DONE]" || agyGenerationJSONHasNoVariants([]byte(value), variants)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !flush() {
				return false
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, hasColon := strings.Cut(line, ":")
		if !hasColon {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		if field == "data" {
			data = append(data, value)
		}
	}
	return scanner.Err() == nil && flush() && events > 0
}

func agyGenerationJSONHasNoSecrets(raw []byte, secrets []string) bool {
	return agyGenerationJSONHasNoVariants(raw, agyGenerationSecretVariants(secrets))
}

func agyGenerationJSONHasNoVariants(raw []byte, variants []string) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var frame json.RawMessage
	if decoder.Decode(&frame) != nil {
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return false
	}
	// Tokens visit every decoded key/value, including overwritten duplicate keys.
	// A map decode would lose earlier reflected values before inspection.
	tokens := json.NewDecoder(bytes.NewReader(frame))
	tokens.UseNumber()
	for {
		token, err := tokens.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if value, ok := token.(string); ok && containsBrokerSecret(value, variants) {
			return false
		}
	}
}

// Match the broker's finite existing representations after JSON decoding.
// This is not a promise to detect arbitrary reversible encodings.
func agyGenerationSecretVariants(secrets []string) []string {
	variants := make([]string, 0, 7*len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		encoded, _ := json.Marshal(secret)
		variants = append(variants, secret, string(encoded[1:len(encoded)-1]), url.QueryEscape(secret), url.PathEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret)), hex.EncodeToString([]byte(secret)))
	}
	return variants
}
