package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/guiyones/folio/internal/platform/db"
	"github.com/jmoiron/sqlx"
)

// Claim picks the next account whose scan is due (not paused, with a
// certificate, next_scan_at at or before now) and, in the same short
// transaction, moves its next_scan_at to reserve. The reservation is
// committed before folio calls SEFAZ, so a crash, a restart or another folio
// process can never query that CNPJ again before reserve. Accounts locked by
// another transaction are skipped. found is false when none is due.
func (s *Store) Claim(ctx context.Context, now, reserve time.Time) (a Account, found bool, err error) {
	const qs = `
	SELECT ` + columns + ` FROM accounts a
	WHERE NOT paused AND next_scan_at <= $1
		AND EXISTS (SELECT 1 FROM certificates c WHERE c.account_id = a.id)
	ORDER BY next_scan_at, id
	LIMIT 1
	FOR UPDATE SKIP LOCKED`

	const qu = `UPDATE accounts SET next_scan_at = $2 WHERE id = $1`

	err = db.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
		var row dbAccount
		if err := tx.GetContext(ctx, &row, qs, now); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claiming due account: %w", db.Map(err))
		}
		if _, err := tx.ExecContext(ctx, qu, row.ID, reserve); err != nil {
			return fmt.Errorf("reserving the next scan: %w", db.Map(err))
		}

		var errConv error
		a, errConv = row.toAccount()
		found = errConv == nil
		return errConv
	})
	if err != nil {
		return Account{}, false, err
	}
	a.NextScanAt = reserve
	return a, found, nil
}

// Lock locks the account inside tx, waiting for a scan that holds it, and
// returns it. Writes to the notes of an account outside the scan take this
// lock first, so the change cursor stays in commit order.
func (s *Store) Lock(ctx context.Context, tx *sqlx.Tx, id uuid.UUID) (Account, error) {
	const q = `SELECT ` + columns + ` FROM accounts WHERE id = $1 FOR UPDATE`

	var row dbAccount
	if err := tx.GetContext(ctx, &row, q, id.String()); err != nil {
		return Account{}, fmt.Errorf("locking account: %w", mapErr(err))
	}
	return row.toAccount()
}

// ScanResult is how a scan of an account ended.
type ScanResult struct {
	UltNSU  int64
	Next    time.Time
	CStat   string
	Message string
}

// FinishScan records how far a scan got, inside a transaction that holds the
// account lock. The stored ult_nsu never goes back, whatever SEFAZ answered.
func (s *Store) FinishScan(ctx context.Context, tx *sqlx.Tx, id uuid.UUID, r ScanResult, now time.Time) error {
	const q = `
	UPDATE accounts SET
		ult_nsu = GREATEST(ult_nsu, $2),
		next_scan_at = $3,
		last_scan_at = $4,
		last_cstat = $5,
		last_message = $6
	WHERE id = $1`

	res, err := tx.ExecContext(ctx, q, id.String(), r.UltNSU, r.Next, now, r.CStat, r.Message)
	if err != nil {
		return fmt.Errorf("finishing scan: %w", db.Map(err))
	}
	return mustAffect(res, ErrNotFound)
}
