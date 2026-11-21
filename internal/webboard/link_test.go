package webboard

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// spend posts link's secret to /api/session as the owner's page does.
func spend(t *testing.T, s *Server, secret string) *http.Response {
	t.Helper()
	q, err := http.NewRequest(http.MethodPost, "http://"+s.Addr()+"/api/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	q.Header.Set("Origin", "http://"+s.Addr())
	if secret != "" {
		q.Header.Set("Authorization", "Bearer "+secret)
	}
	r, err := noRedirect.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	return r
}

func linkSecret(t *testing.T, s *Server) string {
	t.Helper()
	link, err := s.Link()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("t")
}

// The link's secret is not the API secret: a process that read the link
// (the footer, the browser opener's argv, the history) cannot call the API
// with it.
func TestLinkSecretIsNotAnAPISecret(t *testing.T) {
	s := startTest(t, nil)
	secret := linkSecret(t, s)
	for _, path := range []string{"/api/board", "/api/events"} {
		if r := get(t, s, "GET", path, "", secret); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s with the link secret: %d", path, r.StatusCode)
		}
	}
	if r := get(t, s, "POST", "/api/act", "", secret); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/act with the link secret: %d", r.StatusCode)
	}
}

// A link works once: spending it gives the page the API secret, and the same
// link spent again is refused and shuts every page out (the API secret is
// replaced), since something other than the owner's page used it first.
// Each Link is a fresh one, so two opens (two tabs) each have their own.
func TestLinkWorksOnceAndASecondUseShutsPagesOut(t *testing.T) {
	s := startTest(t, nil)
	secret := linkSecret(t, s)
	second := linkSecret(t, s)
	if second == secret {
		t.Fatal("two opens got the same link")
	}
	r := spend(t, s, secret)
	var got struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil || r.StatusCode != http.StatusOK || got.Session == "" {
		t.Fatalf("spend: %d %v %+v", r.StatusCode, err, got)
	}
	checkHeaders(t, "spend", r)
	if r := get(t, s, "GET", "/api/board", "", got.Session); r.StatusCode != http.StatusOK {
		t.Fatalf("the page's API secret: %d", r.StatusCode)
	}
	// A second tab opened from its own link joins without shutting the
	// first out.
	if r := spend(t, s, second); r.StatusCode != http.StatusOK {
		t.Fatalf("the second tab's link: %d", r.StatusCode)
	}
	if r := get(t, s, "GET", "/api/board", "", got.Session); r.StatusCode != http.StatusOK {
		t.Fatalf("a second tab shut the first out: %d", r.StatusCode)
	}
	fresh := linkSecret(t, s)
	if fresh == secret {
		t.Fatal("a spent link was handed out again")
	}
	r = spend(t, s, secret)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a spent link was accepted again: %d", r.StatusCode)
	}
	var refusal Result
	if err := json.NewDecoder(r.Body).Decode(&refusal); err != nil || !strings.Contains(refusal.Text, "already used") {
		t.Fatalf("refusal %+v %v", refusal, err)
	}
	if r := get(t, s, "GET", "/api/board", "", got.Session); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a page that spent a reused link kept access: %d", r.StatusCode)
	}
	// The owner opens a fresh link and is in again.
	r = spend(t, s, fresh)
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("fresh link: %d %v", r.StatusCode, err)
	}
	if r := get(t, s, "GET", "/api/board", "", got.Session); r.StatusCode != http.StatusOK {
		t.Fatalf("the fresh page's API secret: %d", r.StatusCode)
	}
}

// /api/session takes only POST from the Board's own origin with a known link.
func TestSessionRefusals(t *testing.T) {
	s := startTest(t, nil)
	secret := linkSecret(t, s)
	for _, bad := range []string{"", "x", strings.Repeat("A", len(secret)), secret[:len(secret)-1], s.token} {
		if r := spend(t, s, bad); r.StatusCode != http.StatusUnauthorized {
			t.Errorf("spend %q: %d", bad, r.StatusCode)
		}
	}
	if r := get(t, s, "GET", "/api/session", "", secret); r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/session: %d", r.StatusCode)
	}
	q, _ := http.NewRequest(http.MethodPost, "http://"+s.Addr()+"/api/session", nil)
	q.Header.Set("Origin", "http://evil.example")
	q.Header.Set("Authorization", "Bearer "+secret)
	r, err := noRedirect.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("foreign origin: %d", r.StatusCode)
	}
	// None of the refusals spent the link.
	if r := spend(t, s, secret); r.StatusCode != http.StatusOK {
		t.Fatalf("the link was spent by a refusal: %d", r.StatusCode)
	}
}
