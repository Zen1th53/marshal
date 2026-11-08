package evidence

import (
	"context"
	"encoding/json"

	"github.com/Zen1th53/code-evidence/internal/store"
)

// Delivery kinds.
const (
	DeliveryDossier = "dossier"
	DeliveryExpand  = "expand"
)

// Delivery is one ledger entry: evidence shown to a caller under a
// receipt. A dossier entry stands for the receipt's own spans and
// predicates; an expansion entry carries what that expansion delivered,
// in the same span and predicate form as a receipt, so expanded evidence
// can be audited and validated like dossier evidence.
type Delivery struct {
	ID         string      `json:"id"`
	Receipt    string      `json:"receipt"`
	Kind       string      `json:"kind"`
	ObservedAt string      `json:"observed_at"`
	SnapshotID string      `json:"snapshot_id"`
	Select     string      `json:"select,omitempty"`
	View       string      `json:"view,omitempty"`
	Offset     int         `json:"offset,omitempty"`
	Spans      []Span      `json:"spans,omitempty"`
	Predicates []Predicate `json:"predicates,omitempty"`
	Records    int         `json:"records"`
	Bytes      int         `json:"bytes"`
	Truncated  bool        `json:"truncated"`
	// Count is how many times this identical delivery was served (ledger
	// reads only; not part of the id).
	Count int `json:"count,omitempty"`
}

// Ledger is the delivery history of one receipt.
type Ledger struct {
	Receipt string     `json:"receipt"`
	Entries []Delivery `json:"entries"`
}

func (e *Engine) recordDelivery(ctx context.Context, d *Delivery, blobs []string) error {
	d.ID, d.Count = "", 0
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	d.ID = "dlv_" + sha(b)[:32]
	// Dossier entries carry no spans/predicates: the receipt holds them.
	return e.store.PutDelivery(ctx, store.DeliveryRow{ID: d.ID, ReceiptID: d.Receipt, Kind: d.Kind, ObservedAt: d.ObservedAt, Body: b}, blobs)
}

// Deliveries returns the ledger of a receipt (re-authorized under the
// engine policy). Dossier entries are filled from the receipt.
func (e *Engine) Deliveries(ctx context.Context, receipt string) (*Ledger, error) {
	r, err := e.loadReceipt(ctx, receipt)
	if err != nil {
		return nil, err
	}
	rows, err := e.store.Deliveries(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	l := &Ledger{Receipt: r.ID, Entries: []Delivery{}}
	for _, row := range rows {
		var d Delivery
		if err := json.Unmarshal(row.Body, &d); err != nil {
			return nil, err
		}
		d.ID, d.Count = row.ID, row.Count
		if d.Kind == DeliveryDossier {
			d.Spans, d.Predicates = r.Spans, r.Predicates
		}
		l.Entries = append(l.Entries, d)
	}
	return l, nil
}

// DeliveredRelation reports whether any entry delivered relation rel
// (e.g. "callers") of subject, in a dossier predicate or an expansion
// view. This is the basis of "no recorded delivery of caller evidence".
func (l *Ledger) DeliveredRelation(rel, subject string) bool {
	for _, d := range l.Entries {
		for _, p := range d.Predicates {
			if p.Relation == rel && p.Subject == subject {
				return true
			}
		}
	}
	return false
}

// DeliveredSource reports whether source bytes of symbol (or, with an
// empty symbol, of path) were delivered.
func (l *Ledger) DeliveredSource(path, symbol string) bool {
	for _, d := range l.Entries {
		for _, s := range d.Spans {
			if (symbol != "" && s.Symbol == symbol) || (symbol == "" && s.Path == path) {
				return true
			}
		}
	}
	return false
}
