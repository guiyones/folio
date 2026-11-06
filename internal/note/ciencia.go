package note

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/platform/db"
)

// CienciaMaxAttempts is how many times folio sends the Ciência of a note
// before giving up and marking it failed.
const CienciaMaxAttempts = 5

// CienciaDue returns up to limit notes of the account whose Ciência is
// pending and due at now, oldest first.
func (s *Store) CienciaDue(ctx context.Context, accountID uuid.UUID, now time.Time, limit int) ([]Note, error) {
	const q = `SELECT ` + noteColumns + ` FROM notes
	WHERE account_id = $1 AND ciencia = 'pendente'
		AND (ciencia_next_at IS NULL OR ciencia_next_at <= $2)
	ORDER BY seq
	LIMIT $3`

	var rows []dbNote
	if err := s.db.SelectContext(ctx, &rows, q, accountID.String(), now, limit); err != nil {
		return nil, fmt.Errorf("querying due ciência: %w", db.Map(err))
	}
	return toNotes(rows)
}

// CienciaSent marks the Ciência of a note as done.
func (s *Store) CienciaSent(ctx context.Context, tx account.LockedTx, noteID uuid.UUID) error {
	const q = `
	UPDATE notes SET ciencia = 'feita', ciencia_next_at = NULL, seq = nextval('note_seq')
	WHERE id = $1`

	return s.exec(ctx, tx, q, noteID.String())
}

// CienciaFailed counts a failed attempt. After CienciaMaxAttempts the
// Ciência is marked failed; before that it is tried again at retry.
func (s *Store) CienciaFailed(ctx context.Context, tx account.LockedTx, noteID uuid.UUID, retry time.Time) error {
	const q = `
	UPDATE notes SET
		ciencia_attempts = ciencia_attempts + 1,
		ciencia = CASE WHEN ciencia_attempts + 1 >= $2 THEN 'falhou' ELSE ciencia END,
		ciencia_next_at = CASE WHEN ciencia_attempts + 1 >= $2 THEN NULL ELSE CAST($3 AS TIMESTAMPTZ) END,
		seq = nextval('note_seq')
	WHERE id = $1`

	return s.exec(ctx, tx, q, noteID.String(), CienciaMaxAttempts, retry)
}

// CanRequestCiencia says whether the client may queue the Ciência of n by
// hand: after it failed, or when it does not apply because the account has
// the automatic Ciência off. Otherwise it returns ErrAutoCiencia, or
// ErrCienciaState followed by the current state.
func CanRequestCiencia(n Note, autoCiencia bool) error {
	switch {
	case n.Ciencia == CienciaFailed:
		return nil
	case n.Ciencia == CienciaNotApplicable && !autoCiencia:
		return nil
	case n.Ciencia == CienciaNotApplicable:
		return ErrAutoCiencia
	default:
		return fmt.Errorf("%w %s", ErrCienciaState, n.Ciencia)
	}
}

// RequestCiencia queues the Ciência of a note by hand, for accounts with the
// automatic Ciência off, or after it failed. The next scan sends it. Only a
// note whose Ciência does not apply or failed, and that is not cancelled or
// already complete, can be queued; anything else gives ErrConflict.
func (s *Store) RequestCiencia(ctx context.Context, tx account.LockedTx, noteID uuid.UUID) error {
	const q = `
	UPDATE notes SET ciencia = 'pendente', ciencia_attempts = 0, ciencia_next_at = NULL, seq = nextval('note_seq')
	WHERE id = $1 AND ciencia IN ('nao_aplica', 'falhou') AND status = 'resumo'`

	res, err := tx.ExecContext(ctx, q, noteID.String())
	if err != nil {
		return fmt.Errorf("requesting ciência: %w", db.Map(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *Store) exec(ctx context.Context, tx account.LockedTx, q string, args ...any) error {
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("updating note: %w", db.Map(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
