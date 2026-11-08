package store

import (
	"context"
	"database/sql"
	"os"
)

// DeliveryRow is one ledger entry: a dossier or an expansion shown to a
// caller under a receipt. Count is how many times the identical delivery
// was served.
type DeliveryRow struct {
	ID         string
	ReceiptID  string
	Kind       string
	ObservedAt string
	Body       []byte
	Count      int
}

// PutDelivery durably records a delivery and its blob reverse index
// before the delivery is returned to the caller. An identical delivery
// (same id) increments its count.
func (s *Store) PutDelivery(ctx context.Context, d DeliveryRow, blobs []string) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO deliveries VALUES (?,?,?,?,?,1,
				(SELECT coalesce(max(seq), 0) + 1 FROM deliveries))
			ON CONFLICT(delivery_id) DO UPDATE SET count = count + 1`,
			d.ID, d.ReceiptID, d.Kind, d.ObservedAt, d.Body); err != nil {
			return err
		}
		tombs, err := tombstones(ctx, tx)
		if err != nil {
			return err
		}
		for _, b := range blobs {
			b = unrevoked(tombs, FileRow{SHA256: b}).SHA256
			// Expansion dependencies belong to the receipt for revocation
			// and purge, including files not read in the original locator.
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO receipt_blobs VALUES (?,?)`, b, d.ReceiptID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO delivery_blobs VALUES (?,?)`, b, d.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// Deliveries lists the ledger entries of a receipt in recording order.
func (s *Store) Deliveries(ctx context.Context, receiptID string) ([]DeliveryRow, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	rows, err := s.db.QueryContext(ctx, `SELECT delivery_id, receipt_id, kind, observed_at, body, count
		FROM deliveries WHERE receipt_id = ? ORDER BY seq`, receiptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryRow
	for rows.Next() {
		var d DeliveryRow
		if err := rows.Scan(&d.ID, &d.ReceiptID, &d.Kind, &d.ObservedAt, &d.Body, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Files returns the database, WAL and shared-memory file paths.
func (s *Store) Files() []string {
	return []string{s.path, s.path + "-wal", s.path + "-shm"}
}

func statFile(p string) (int64, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// ReceiptBlobs includes dependencies delivered after the original receipt.
func (s *Store) ReceiptBlobs(ctx context.Context, receiptID string) ([]string, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return scanStrings(s.db.QueryContext(ctx, `SELECT sha256 FROM receipt_blobs WHERE receipt_id = ?
 UNION SELECT db.sha256 FROM delivery_blobs db JOIN deliveries d USING(delivery_id) WHERE d.receipt_id = ?`, receiptID, receiptID))
}
