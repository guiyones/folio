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

		claimed, err := row.toAccount()
		if err != nil {
			return err
		}
		a, found = claimed, true
		return nil
	})
	if err != nil {
		return Account{}, false, err
	}
	a.NextScanAt = reserve
	return a, found, nil
}

// LockedTx is a transaction that holds the lock of one account. Writes to
// the notes of an account take one, and only WithLock makes it, so a write
// cannot forget the lock that keeps the change cursor in commit order.
type LockedTx struct {
	*lockedTx
	Account Account // the account as read under the lock
}

// lockedTx keeps the transaction field unexported: code outside this
// package uses its methods but cannot build a LockedTx around any tx.
type lockedTx = sqlx.Tx

// WithLock runs fn in a transaction that holds the lock of the account,
// waiting for a scan that holds it. The transaction commits when fn returns
// nil and rolls back otherwise.
func (s *Store) WithLock(ctx context.Context, id uuid.UUID, fn func(tx LockedTx) error) error {
	return db.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
		const q = `SELECT ` + columns + ` FROM accounts WHERE id = $1 FOR UPDATE`

		var row dbAccount
		if err := tx.GetContext(ctx, &row, q, id.String()); err != nil {
			return fmt.Errorf("locking account: %w", mapErr(err))
		}
		a, err := row.toAccount()
		if err != nil {
			return err
		}
		return fn(LockedTx{lockedTx: tx, Account: a})
	})
}

// ScanResult is how a scan of an account ended.
type ScanResult struct {
	UltNSU  int64
	Next    time.Time
	CStat   string
	Message string
}

// FinishScan records how far a scan of the locked account got. The stored
// ult_nsu never goes back, whatever SEFAZ answered.
func (s *Store) FinishScan(ctx context.Context, tx LockedTx, r ScanResult, now time.Time) error {
	const q = `
	UPDATE accounts SET
		ult_nsu = GREATEST(ult_nsu, $2),
		next_scan_at = $3,
		last_scan_at = $4,
		last_cstat = $5,
		last_message = $6
	WHERE id = $1`

	res, err := tx.ExecContext(ctx, q, tx.Account.ID.String(), r.UltNSU, r.Next, now, r.CStat, r.Message)
	if err != nil {
		return fmt.Errorf("finishing scan: %w", db.Map(err))
	}
	return db.MustAffect(res, ErrNotFound)
}
