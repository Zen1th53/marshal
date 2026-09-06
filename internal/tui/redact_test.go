package tui

import (
	"strings"
	"testing"
)

// TestRedactionDoesNotDependOnSecretLength is the regression for the audit
// finding that "password=hunter2" reached the store in cleartext because the
// pattern required eight or more characters. Sensitivity belongs to the key,
// not to how long the value happens to be.
func TestRedactionDoesNotDependOnSecretLength(t *testing.T) {
	secrets := []struct{ name, input, leaked string }{
		{"one char", "password=x", "x"},
		{"short", "password=hunter2", "hunter2"},
		{"api_key short", "api_key=abc", "abc"},
		{"token numeric", "token=1234", "1234"},
		{"secret word", "secret=foo", "foo"},
		{"authorization", "authorization=Bearer x", "Bearer x"},
		{"client secret", "client_secret: s3", "s3"},
		{"colon form", "password: pw", "pw"},
	}

	for _, s := range secrets {
		out := RedactContent(s.input, nil)
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("%s: %q was not redacted -> %q", s.name, s.input, out)
			continue
		}
		if strings.Contains(out, s.leaked) {
			t.Errorf("%s: secret value %q survived redaction -> %q", s.name, s.leaked, out)
		}
	}
}

// TestRedactionLeavesOrdinaryTextAlone guards against over-redaction: prose that
// merely mentions a sensitive word must survive intact.
func TestRedactionLeavesOrdinaryTextAlone(t *testing.T) {
	harmless := []string{
		"the password policy requires rotation",
		"rotate the api key next quarter",
		"a token of appreciation",
		"secret santa is on friday",
		"/inspect claim C-01",
	}

	for _, in := range harmless {
		if out := RedactContent(in, nil); out != in {
			t.Errorf("ordinary text was altered:\n  in:  %q\n  out: %q", in, out)
		}
	}
}

// TestRedactionCoversBearerAndKeyMaterial keeps the existing patterns working.
// The provider-key fixture is assembled at run time so this test file carries no
// literal that resembles a real credential.
func TestRedactionCoversBearerAndKeyMaterial(t *testing.T) {
	if out := RedactContent("Authorization: Bearer abcdefghijklmnop", nil); strings.Contains(out, "abcdefghijklmnop") {
		t.Errorf("bearer token survived: %q", out)
	}

	body := "abcdefghijklmnopqrst"
	fixture := "use " + "sk-" + body + " here"
	if out := RedactContent(fixture, nil); strings.Contains(out, body) {
		t.Errorf("provider key survived: %q", out)
	}
}

// TestRedactionAppliesToRenderedSurfaces proves the same redaction covers the
// surfaces an operator actually reads, not just the composer.
func TestRedactionAppliesToRenderedSurfaces(t *testing.T) {
	th := NewTheme(ThemeNoColor, false, false)
	state := UIState{
		ProjectID:      "proj",
		ActiveBlocker:  "deploy blocked: password=hunter2",
		ActiveQuestion: "confirm with api_key=abc",
	}

	rendered := StripANSI(strings.Join(buildBody(state, th, 100, 40), "\n"))
	for _, leaked := range []string{"hunter2"} {
		if strings.Contains(rendered, leaked) {
			t.Errorf("secret %q rendered into the workspace body:\n%s", leaked, rendered)
		}
	}
}
