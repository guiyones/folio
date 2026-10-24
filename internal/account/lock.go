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

// LockDue opens a transaction and locks the next account whose scan is due:
// not paused, with a certificate and next_scan_at at or before now. Accounts
// locked by another transaction are skipped, so two folio processes never
// scan the same account. found is false when no account is due, and then no
// transaction is left open.
//
// The caller owns tx: it must Commit or Rollback it.
func (s *Store) LockDue(ctx context.Context, now time.Time) (tx *sqlx.Tx, a Account, found bool, err error) {
	tx, err = s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, Account{}, false, fmt.Errorf("beginning transaction: %w", err)
	}

	const q = `
	SELECT ` + columns + ` FROM accounts a
	WHERE NOT paused AND next_scan_at <= $1
		AND EXISTS (SELECT 1 FROM certificates c WHERE c.account_id = a.id)
	ORDER BY next_scan_at, id
	LIMIT 1
	FOR UPDATE SKIP LOCKED`

	var row dbAccount
	if err := tx.GetContext(ctx, &row, q, now); err != nil {
		errRb := db.Rollback(tx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, Account{}, false, errRb
		}
		return nil, Account{}, false, errors.Join(fmt.Errorf("locking due account: %w", db.Map(err)), errRb)
	}

	a, err = row.toAccount()
	if err != nil {
		return nil, Account{}, false, errors.Join(err, db.Rollback(tx))
	}
	return tx, a, true, nil
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

// FinishScan records the end of a scan inside tx. The stored ult_nsu never
// goes back, whatever SEFAZ answered.
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
