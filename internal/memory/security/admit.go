package security

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/redaction"
)

// RedactionMetaKey names the memory metadata that records which kinds of
// secret were removed from a record before it was kept. It never holds a value.
const RedactionMetaKey = "firewall_redacted"

// Redaction reports what Redact removed: the kinds of secret detected, never
// the values. A zero Redaction means nothing was removed.
type Redaction struct{ Kinds []string }

func (r Redaction) Applied() bool { return len(r.Kinds) > 0 }

func (r *Redaction) add(kind string) {
	for _, k := range r.Kinds {
		if k == kind {
			return
		}
	}
	r.Kinds = append(r.Kinds, kind)
	sort.Strings(r.Kinds)
}

func (r *Redaction) merge(o Redaction) {
	for _, k := range o.Kinds {
		r.add(k)
	}
}

// Redact returns text with every detected secret replaced by
// redaction.Marker. Values are replaced in place where a detector can locate
// them; a line a detector still recognizes is replaced whole, and as a last
// resort the whole text. The result never contains a detected secret.
func (f *Firewall) Redact(text string) (string, Redaction) {
	var r Redaction
	kind := f.detectSecret(text)
	if kind == "" {
		return text, r
	}
	r.add(kind)
	for _, literal := range f.config.CanarySecrets {
		if literal != "" {
			text = strings.ReplaceAll(text, literal, redaction.Marker)
		}
	}
	for _, reg := range f.config.CustomRegexPatterns {
		if reg != nil {
			text = reg.ReplaceAllString(text, redaction.Marker)
		}
	}
	text = redaction.RedactAll(text)
	if kind = f.detectSecret(text); kind == "" {
		return text, r
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if k := f.detectSecret(line); k != "" {
			r.add(k)
			lines[i] = redaction.Marker
		}
	}
	text = strings.Join(lines, "\n")
	if kind = f.detectSecret(text); kind != "" {
		r.add(kind)
		text = redaction.Marker
	}
	return text, r
}

// RedactJSON redacts every string (keys and values) of a JSON document and
// re-encodes it. Documents with no detected secret are returned unchanged.
func (f *Firewall) RedactJSON(raw []byte) ([]byte, Redaction, error) {
	var r Redaction
	if f.detectSecret(string(raw)) == "" {
		return raw, r, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, r, err
	}
	doc = f.redactValue(doc, &r)
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, r, err
	}
	if kind := f.detectSecret(string(out)); kind != "" {
		// A secret split across fields: nothing of the document is kept.
		r.add(kind)
		out, err = json.Marshal(redaction.Marker)
	}
	return out, r, err
}

func (f *Firewall) redactValue(v any, r *Redaction) any {
	switch t := v.(type) {
	case string:
		s, got := f.Redact(t)
		r.merge(got)
		return s
	case []any:
		for i := range t {
			t[i] = f.redactValue(t[i], r)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			key, got := f.Redact(k)
			r.merge(got)
			out[key] = f.redactValue(val, r)
		}
		return out
	}
	return v
}

// AdmitText applies the edition's secret policy to text MARSHAL is about to
// keep or hand on. Community refuses it (the ScanText error); Extreme removes
// the secret and continues, reporting what was removed.
func (f *Firewall) AdmitText(text string) (string, Redaction, error) {
	if !RedactsSecrets {
		return text, Redaction{}, f.ScanText(text)
	}
	out, r := f.Redact(text)
	return out, r, nil
}

// AdmitJSON is AdmitText for a JSON document; Extreme keeps it valid JSON.
func (f *Firewall) AdmitJSON(raw []byte) ([]byte, Redaction, error) {
	if !RedactsSecrets {
		return raw, Redaction{}, f.ScanText(string(raw))
	}
	return f.RedactJSON(raw)
}

// AdmitRecord applies the edition's secret policy to a memory record.
// Community refuses it (the ScanRecord error). Extreme redacts title, body,
// source reference and metadata, records the redacted kinds under
// RedactionMetaKey, and still refuses a credential-shaped identifier, which
// cannot be rewritten without changing what the record is.
func (f *Firewall) AdmitRecord(ctx context.Context, rec model.MemoryRecordV2) (model.MemoryRecordV2, Redaction, error) {
	if !RedactsSecrets {
		return rec, Redaction{}, f.ScanRecord(ctx, rec)
	}
	if err := ctx.Err(); err != nil {
		return rec, Redaction{}, err
	}
	var r Redaction
	var got Redaction
	rec.Title, got = f.Redact(rec.Title)
	r.merge(got)
	rec.Body, got = f.Redact(rec.Body)
	r.merge(got)
	rec.Source.Reference, got = f.Redact(rec.Source.Reference)
	r.merge(got)
	if rec.ExtMeta != nil {
		raw, err := json.Marshal(rec.ExtMeta)
		if err != nil {
			return rec, r, err
		}
		if f.detectSecret(string(raw)) != "" {
			out, metaRedaction, err := f.RedactJSON(raw)
			if err != nil {
				return rec, r, err
			}
			meta := map[string]any{}
			if err := json.Unmarshal(out, &meta); err != nil {
				meta = map[string]any{"redacted": redaction.Marker}
			}
			rec.ExtMeta = meta
			r.merge(metaRedaction)
		}
	}
	if r.Applied() {
		meta := make(map[string]any, len(rec.ExtMeta)+1)
		for k, v := range rec.ExtMeta {
			meta[k] = v
		}
		kinds := make([]any, 0, len(r.Kinds))
		for _, k := range r.Kinds {
			kinds = append(kinds, k)
		}
		meta[RedactionMetaKey] = kinds
		rec.ExtMeta = meta
	}
	if err := f.ScanRecord(ctx, rec); err != nil {
		return rec, r, fmt.Errorf("after redaction: %w", err)
	}
	return rec, r, nil
}
