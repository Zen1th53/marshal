package architecturecodec_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCommunityArchitectureReceiptOriginalContract(t *testing.T) {
	// Encode the 723540f contract independently: declared field order, uppercase
	// names, base64 byte slices, and domain NUL JSON commitments.
	key := ed25519.NewKeyFromSeed(bytes32(11))
	hash := func(domain, payload string) string {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join([]string{domain, payload}, "\x00"))))
	}
	target := `"ProjectID":"PROJECT-original","Root":"/tmp/original","StorePath":"/tmp/original/.marshal/state.db","SourceRoot":"/tmp/original","Protocol":"architecture-review.v1","Policy":"community"`
	generation := fmt.Sprintf(`{%s,"Generation":1,"GitDirectory":"/tmp/original/.git","Issuer":%q,"CommandIssuer":null,"EnrollmentID":"original-enrollment"}`, target, base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	enrollmentSignature := ed25519.Sign(key, []byte("marshal.architecture.enrollment.v1\x00"+generation))
	enrollmentWire := fmt.Sprintf(`{"Target":%s,"Signature":%q}`, generation, base64.StdEncoding.EncodeToString(enrollmentSignature))
	var enrollment architecturecodec.SignedArchitectureEnrollment
	if err := json.Unmarshal([]byte(enrollmentWire), &enrollment); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.Verify(key.Public().(ed25519.PublicKey)); err != nil {
		t.Fatalf("original enrollment: %v", err)
	}
	binding := func(id, receipt, envelope, origin string) string {
		return fmt.Sprintf(`{"SourceDirty":false,%s,"ArchitectureID":"original-architecture","Revision":2,"Digest":%q,"LayoutDigest":%q,"UIDigest":%q,"SourceHead":%q,"SourceTree":%q,"ReceiptID":%q,"ReceiptDigest":%q,"EnvelopeDigest":%q,"Origin":%q}`, target, strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 40), strings.Repeat("e", 64), id, receipt, envelope, origin)
	}
	now := time.Unix(1800000000, 0)
	challenge := func(binding string) string {
		return fmt.Sprintf(`{"Generation":1,"Binding":%s,"SourceDirty":false,"Nonce":%q,"SessionID":"original-enrollment","MessageID":"original-message","Message":"Accept this exact architecture","ExpiresUnix":%d}`, binding, strings.Repeat("f", 64), now.Add(3*time.Minute).Unix())
	}
	envelope := hash("marshal.architecture.selection.v1", binding("", "", "", ""))
	receipt := hash("marshal.architecture.challenge.v1", challenge(binding("original-receipt", "", envelope, "owner_browser")))
	payload := challenge(binding("original-receipt", receipt, envelope, "owner_browser"))
	signature := ed25519.Sign(key, []byte("marshal.architecture.receipt.v1\x00"+payload))
	wire := []byte(fmt.Sprintf(`{"Challenge":%s,"Signature":%q}`, payload, base64.StdEncoding.EncodeToString(signature)))
	decoded, err := architecturecodec.DecodeSignedArchitectureReceipt(wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.Verify(enrollment.Target, now); err != nil {
		t.Fatalf("original browser receipt: %v", err)
	}
	roundTrip, err := json.Marshal(decoded)
	if err != nil || !bytes.Equal(roundTrip, wire) {
		t.Fatalf("original receipt wire changed: %s (%v)", roundTrip, err)
	}
}

func TestCommunityArchitectureReceiptMalformedFactsReturnPolicyError(t *testing.T) {
	for _, constraints := range []string{"", `,"constraints":null`, `,"constraints":{}`, `,"constraints":"invalid"`, `,"constraints":1`} {
		t.Run(constraints, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("malformed facts panicked: %v", recovered)
				}
			}()
			graph := json.RawMessage(`{"title":"Test","purpose":"Test","components":[{"id":"api","name":"API","responsibility":"Serve","technology":"Go"}],"connections":[]` + constraints + `}`)
			if _, err := architecturecodec.ArchitectureFactDigests(graph); !errors.Is(err, model.ErrPolicyDenied) {
				t.Fatalf("malformed facts did not return policy denial: %v", err)
			}
		})
	}
}

// The seed and wire field names are fixture data, not a product issuer.
func syntheticReceipt(t *testing.T) (architecturecodec.ArchitectureTargetGeneration, []byte) {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes32(7))
	pub := priv.Public().(ed25519.PublicKey)
	h := strings.Repeat("a", 64)
	g := architecturecodec.ArchitectureTargetGeneration{
		ArchitectureTarget: architecturecodec.ArchitectureTarget{ProjectID: "PROJECT-test", Root: "/tmp/owner-repo", SourceRoot: "/tmp/owner-repo", StorePath: "/tmp/owner-repo/.marshal/state.db", Protocol: architecturecodec.Version, Policy: "community"},
		Generation:         1, GitDirectory: "/tmp/owner-repo/.git", Issuer: pub, EnrollmentID: "enrollment-1",
	}
	c := architecturecodec.ArchitectureOwnerChallenge{Generation: 1, SourceDirty: false, Nonce: h, SessionID: g.EnrollmentID, MessageID: "message-1", Message: "Accept this exact architecture", ExpiresUnix: time.Now().Add(3 * time.Minute).Unix()}
	c.Binding = architecturecodec.ArchitectureAcceptance{ArchitectureTarget: g.ArchitectureTarget, ArchitectureID: "architecture-1", Revision: 2, Digest: h, LayoutDigest: h, UIDigest: h, SourceHead: strings.Repeat("b", 40), SourceTree: h, ReceiptID: "receipt-1", Origin: "owner_browser"}
	architecturecodec.BindArchitectureChallenge(&c)
	challenge, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, append([]byte("marshal.architecture.receipt.v1\x00"), challenge...))
	// Literal uppercase envelope keys and Go's base64 byte encoding are part
	// of the original wire contract.
	wire := []byte(fmt.Sprintf(`{"Challenge":%s,"Signature":%q}`, challenge, base64String(sig)))
	return g, wire
}

func bytes32(value byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = value
	}
	return b
}
func base64String(b []byte) string { raw, _ := json.Marshal(b); return strings.Trim(string(raw), `"`) }

func TestCommunityArchitectureReceiptRejectsPrivateCommandApproval(t *testing.T) {
	g, wire := syntheticReceipt(t)
	r, err := architecturecodec.DecodeSignedArchitectureReceipt(wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(g, time.Now()); err != nil {
		t.Fatalf("browser receipt: %v", err)
	}
	r.Challenge.Binding.Origin = "owner_command"
	if err := r.Verify(g, time.Now()); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("command origin accepted: %v", err)
	}
	commandKey := ed25519.NewKeyFromSeed(bytes32(9))
	g.Policy = "private_extreme"
	g.CommandIssuer = commandKey.Public().(ed25519.PublicKey)
	r.Challenge.Binding.ArchitectureTarget = g.ArchitectureTarget
	architecturecodec.BindArchitectureChallenge(&r.Challenge)
	challenge, err := json.Marshal(r.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	r.Signature = ed25519.Sign(commandKey, append([]byte("marshal.architecture.receipt.v1\x00"), challenge...))
	if err := r.Verify(g, time.Now()); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("valid private command signature accepted by Community codec: %v", err)
	}
	if _, err := architecturecodec.DecodeSignedArchitectureReceipt([]byte(`{"challenge":{},"Signature":""}`)); err == nil {
		t.Fatal("lowercase alias accepted")
	}
}

func TestCommunityArchitectureReceiptBindsCanonicalSourceAndStore(t *testing.T) {
	g, wire := syntheticReceipt(t)
	r, err := architecturecodec.DecodeSignedArchitectureReceipt(wire)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*architecturecodec.SignedArchitectureReceipt){
		"tree": func(r *architecturecodec.SignedArchitectureReceipt) {
			r.Challenge.Binding.SourceTree = strings.Repeat("c", 64)
		},
		"head": func(r *architecturecodec.SignedArchitectureReceipt) {
			r.Challenge.Binding.SourceHead = strings.Repeat("c", 40)
		},
		"store": func(r *architecturecodec.SignedArchitectureReceipt) { r.Challenge.Binding.StorePath += ".other" },
		"layout": func(r *architecturecodec.SignedArchitectureReceipt) {
			r.Challenge.Binding.LayoutDigest = strings.Repeat("c", 64)
		},
		"ui": func(r *architecturecodec.SignedArchitectureReceipt) {
			r.Challenge.Binding.UIDigest = strings.Repeat("c", 64)
		},
		"dirty": func(r *architecturecodec.SignedArchitectureReceipt) { r.Challenge.SourceDirty = true },
	} {
		t.Run(name, func(t *testing.T) {
			changed := r
			mutate(&changed)
			if err := changed.Verify(g, time.Now()); err == nil {
				t.Fatal("mutated signed field accepted")
			}
		})
	}
}
