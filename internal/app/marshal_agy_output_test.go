package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Agy 1.3.2 can put multiple JSON messages in response while returning the
// schema-constrained answer separately in structured_output.
func TestMarshalAgyPrefersStructuredOutput(t *testing.T) {
	data := []byte(`{"conversation_id":"agy-real-envelope","status":"SUCCESS","response":"{\"tasks\":[]}\n{\"tasks\":[],\"toolAction\":\"Completing task\"}","structured_output":{"tasks":[]}}`)
	payload, id, err := marshalCLIOutput("agy", data)
	if err != nil || id != "agy-real-envelope" || !json.Valid(payload) {
		t.Fatalf("structured answer unavailable: payload=%s session=%q err=%v", payload, id, err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(payload, &got); err != nil || len(got) != 1 || string(got["tasks"]) != "[]" {
		t.Fatalf("wrong structured answer: %s (%v)", payload, err)
	}
}

func TestMarshalVerifierInputIdentifiesPreDeliveryStage(t *testing.T) {
	const goal = "Deliver the verified README.md to the target branch after approval"
	data, err := verifierInput(marshal.Run{GoalBinding: goal}, "candidate-head", verification.Session{})
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Stage string
		Head  string
		Goal  string
	}
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	if input.Stage != "pre_delivery" || input.Head != "candidate-head" || input.Goal != goal {
		t.Fatalf("verifier cannot distinguish candidate verification from delivery: %+v", input)
	}
}

func TestMarshalAgyRefusesFailedEnvelope(t *testing.T) {
	for _, data := range []string{
		`{"conversation_id":"failed","status":"ERROR","response":"{\"Verdict\":\"accept\"}"}`,
		`{"conversation_id":"failed","status":"ERROR","structured_output":{"Verdict":"accept"}}`,
	} {
		if _, _, err := marshalCLIOutput("agy", []byte(data)); err == nil {
			t.Fatal("failed provider turn was accepted")
		}
	}
}

func TestMarshalAgyExplainsDeniedEmptyTurn(t *testing.T) {
	data := []byte(`{"conversation_id":"denied","status":"SUCCESS","response":"","denied_actions":[{"action":"command","display_name":"RunCommand"}]}`)
	_, _, err := marshalCLIOutput("agy", data)
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("empty denied turn lost its cause: %v", err)
	}
}

func TestMarshalAgyResponseFallbackAndMissingAnswer(t *testing.T) {
	data := []byte(`{"conversation_id":"agy-response","status":"SUCCESS","structured_output":null,"response":"{\"ok\":true}"}`)
	payload, id, err := marshalCLIOutput("agy", data)
	if err != nil || id != "agy-response" || string(payload) != `{"ok":true}` {
		t.Fatalf("response fallback failed: %s %q %v", payload, id, err)
	}
	for _, data := range []string{`{`, `{"status":"SUCCESS","response":""}`} {
		if _, _, err := marshalCLIOutput("agy", []byte(data)); err == nil {
			t.Fatalf("missing or malformed answer was accepted: %s", data)
		}
	}
}
