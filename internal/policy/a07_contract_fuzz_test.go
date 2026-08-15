package policy

import (
	"testing"
)

func TestA07DecisionRejectsMalformedBinding(t *testing.T) {
	valid := Policy{ID: "a07", Version: 1, Default: EffectDeny}
	digest, err := valid.Digest()
	if err != nil {
		t.Fatal(err)
	}
	decision := Decision{
		PolicyDigest: digest,
		Binding:      PolicyBinding{Version: 0, Digest: digest},
		Effect:       EffectDeny,
	}
	if err := decision.Validate(); err == nil {
		t.Fatal("decision with malformed policy binding was accepted")
	}
}

func TestA07PolicyRejectsInvalidUTF8Identity(t *testing.T) {
	p := Policy{ID: PolicyID(string([]byte{'f', 0xff})), Version: 1, Default: EffectDeny}
	if err := p.Validate(); err == nil {
		t.Fatal("invalid UTF-8 policy identity was accepted")
	}
}

func TestA07CanonicalizationRejectsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	policy := Policy{ID: "a07", Version: 1, Rules: []Rule{{
		ID: invalid, Description: "safe", Effect: EffectDeny,
	}}}
	if err := policy.Validate(); err == nil {
		t.Fatal("policy with invalid UTF-8 identifier was accepted")
	}
}

func FuzzA07PolicyDigestValidation(f *testing.F) {
	f.Add("sha256:" + "0000000000000000000000000000000000000000000000000000000000000000")
	f.Add("SHA256:bad")
	f.Add("sha256:")
	f.Fuzz(func(t *testing.T, value string) {
		err := PolicyDigest(value).Validate()
		if err == nil && len(value) != len("sha256:")+64 {
			t.Fatalf("accepted non-canonical digest length %d", len(value))
		}
	})
}

func FuzzA07PolicyStateValidation(f *testing.F) {
	f.Add("loaded")
	f.Add("active")
	f.Add("ACTIVE")
	f.Fuzz(func(t *testing.T, value string) {
		state := State(value)
		if state.Valid() && !CanTransition(state, StateValidated) && state == StateLoaded {
			t.Fatal("valid initial state lost its legal transition")
		}
	})
}

func FuzzA07PolicyParseRoundTrip(f *testing.F) {
	f.Add([]byte("id: p\nversion: 1\ndefault: deny\nrules: []\n"))
	f.Add([]byte("id: p\nversion: 1\nrules:\n- id: deny\n  description: safe\n  effect: deny\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		parsed, err := Parse(input)
		if err != nil {
			return
		}
		canonical, err := parsed.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		reparsed, err := Parse(canonical)
		if err != nil {
			t.Fatalf("canonical policy did not parse: %v", err)
		}
		first, err := parsed.Digest()
		if err != nil {
			t.Fatal(err)
		}
		second, err := reparsed.Digest()
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Fatalf("digest changed after canonical round trip: %s != %s", first, second)
		}
	})
}

func FuzzA07DecisionValidation(f *testing.F) {
	f.Add(true, "allow", "", "")
	f.Add(false, "deny", "denied", "")
	f.Add(false, "require", "", "REQUIRE_APPROVAL")
	f.Fuzz(func(t *testing.T, allowed bool, effect, deniedBy, requirement string) {
		policy := Policy{ID: "a07", Version: 1, Default: EffectDeny}
		digest, err := policy.Digest()
		if err != nil {
			t.Fatal(err)
		}
		d := Decision{
			Allowed:      allowed,
			DeniedBy:     deniedBy,
			PolicyDigest: digest,
			Binding:      PolicyBinding{Version: policy.Version, Digest: digest},
			Effect:       Effect(effect),
		}
		if requirement != "" {
			d.Requirements = []Obligation{Obligation(requirement)}
		}
		if err := d.Validate(); err == nil {
			if err := d.Binding.Validate(); err != nil || d.Binding.Digest != d.PolicyDigest {
				t.Fatal("validated decision has invalid binding")
			}
		}
	})
}
