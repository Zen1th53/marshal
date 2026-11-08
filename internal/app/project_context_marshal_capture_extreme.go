//go:build !governed

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/store"
)

// Neither type exports fields or admits JSON restoration. The live runtime
// pointer, exact CLI and consumed actual turn are indispensable. Durable
// receipts and caller-provided verifier results alone cannot create this proof.
type requiredMarshalTurnPreparation struct {
	runtime                                     *Runtime
	cli                                         *MarshalCLI
	receipt                                     projectcontext.Receipt
	admission                                   store.ProjectContextAdmission
	workspace, head, tree                       string
	binaryCommandPath, binaryPath, binaryDigest string
}

type requiredMarshalTurnCapture struct {
	preparation                              requiredMarshalTurnPreparation
	provider, session, priorSession          string
	binaryDigest, observedModel              string
	inputDigest, outputDigest, payloadDigest string
	startedAt, finishedAt                    time.Time
	consumed                                 bool
}

func (m *MarshalCLI) resetRequiredMarshalTurn() {
	m.requiredTurnCapture, m.requiredTurnPreparation = nil, nil
}

func (*MarshalCLI) decodeRequiredMarshalResult(payload []byte, out any) error {
	value := reflect.ValueOf(out)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("Marshal result requires a destination")
	}
	decoded := reflect.New(value.Elem().Type())
	if err := proposalmcp.DecodeStrict(payload, decoded.Interface()); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var wire any
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := requiredMarshalExactResultKeys(wire, value.Elem().Type()); err != nil {
		return err
	}
	value.Elem().Set(decoded.Elem())
	return nil
}

// encoding/json intentionally folds struct-field case. A governed result must
// have one exact typed spelling, including inside arrays and nested objects.
func requiredMarshalExactResultKeys(wire any, kind reflect.Type) error {
	// Opaque architecture DATA is validated by its dedicated proposal/selection
	// parser; it has no typed fields that could acquire authority by case folding.
	if kind == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	if wire == nil {
		return nil
	}
	switch object := wire.(type) {
	case map[string]any:
		if kind.Kind() == reflect.Map {
			for _, value := range object {
				if err := requiredMarshalExactResultKeys(value, kind.Elem()); err != nil {
					return err
				}
			}
			return nil
		}
		if kind.Kind() != reflect.Struct {
			return errors.New("Marshal result object has no exact typed schema")
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for name, value := range object {
			field, ok := fields[name]
			if !ok {
				return errors.New("Marshal result field spelling does not match its exact schema")
			}
			if err := requiredMarshalExactResultKeys(value, field); err != nil {
				return err
			}
		}
	case []any:
		if kind.Kind() != reflect.Slice && kind.Kind() != reflect.Array {
			return errors.New("Marshal result array has no exact typed schema")
		}
		for _, value := range object {
			if err := requiredMarshalExactResultKeys(value, kind.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Called only after a successful actual subprocess, current watcher/source
// finish and typed result decoding. A fresh provider session is required later
// for architecture review; missing provenance remains missing here. Input
// hashes cover the entire prompt/schema. This does not assert an observed model
// identity, hidden provider instructions, paid tokens or native approval.
func (m *MarshalCLI) captureRequiredMarshalTurn(prompt, schema string, raw, payload []byte, priorSession, session string, startedAt, finishedAt time.Time) {
	p := m.requiredTurnPreparation
	if p == nil || p.runtime != m.runtime || p.cli != m || p.receipt.Outcome != "QUALIFIED" || startedAt.IsZero() || finishedAt.Before(startedAt) {
		return
	}
	m.requiredTurnCapture = &requiredMarshalTurnCapture{
		preparation: *p, provider: m.Provider, session: session, priorSession: priorSession,
		binaryDigest: p.binaryDigest, observedModel: requiredMarshalObservedModel(m.Provider, raw),
		inputDigest:   projectcontext.Digest("marshal.required.actual-model-input.v1", struct{ Provider, Prompt, Schema string }{m.Provider, prompt, schema}),
		outputDigest:  projectcontext.Digest("marshal.required.actual-model-output.v1", raw),
		payloadDigest: projectcontext.Digest("marshal.required.actual-model-payload.v1", payload),
		startedAt:     startedAt, finishedAt: finishedAt,
	}
}

// Exact trusted composition takes this only once. A different CLI or runtime
// cannot consume the rightful owner's proof. A resumed/missing session cannot
// certify a new independent architectural review.
func (m *MarshalCLI) takeRequiredMarshalTurnCapture(r *Runtime) (*requiredMarshalTurnCapture, error) {
	if m == nil {
		return nil, projectcontext.ErrMissing
	}
	p := m.requiredTurnCapture
	if p == nil || r == nil || p.preparation.runtime != r || p.preparation.cli != m || m.runtime != r {
		return nil, projectcontext.ErrMissing
	}
	if p.consumed {
		return nil, projectcontext.Changed("project_context_marshal_capture_extreme.go:161")
	}
	p.consumed = true
	if p.session == "" || p.priorSession != "" || p.session == p.priorSession {
		return nil, projectcontext.ErrReviewPending
	}
	return p, nil
}
