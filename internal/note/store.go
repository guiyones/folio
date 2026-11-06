package note

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/platform/db"
	"github.com/jmoiron/sqlx"
)

// MaxPage bounds a page of NotesAfter.
const MaxPage = 500

// Store keeps documents, notes and events in Postgres.
type Store struct {
	db *sqlx.DB
}

// NewStore returns a Store over db.
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db}
}

// SaveDoc stores a document received under an NSU. A repeated NSU returns
// the stored document with isNew false, so a scan that runs again after a
// failure does not apply it twice.
func (s *Store) SaveDoc(ctx context.Context, tx account.LockedTx, accountID uuid.UUID, d dfe.Doc, now time.Time) (uuid.UUID, bool, error) {
	const qi = `
	INSERT INTO dfe_docs (id, account_id, nsu, schema, xml, received_at)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (account_id, nsu) DO NOTHING
	RETURNING id`

	var id string
	err := tx.GetContext(ctx, &id, qi, uuid.NewV7().String(), accountID.String(), d.NSU, d.Schema, d.XML, now)
	switch {
	case err == nil:
		docID, err := parseDocID(id)
		if err != nil {
			return uuid.Nil(), false, err
		}
		return docID, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return uuid.Nil(), false, fmt.Errorf("storing doc %d: %w", d.NSU, db.Map(err))
	}

	const qs = `SELECT id FROM dfe_docs WHERE account_id = $1 AND nsu = $2`
	if err := tx.GetContext(ctx, &id, qs, accountID.String(), d.NSU); err != nil {
		return uuid.Nil(), false, fmt.Errorf("querying doc %d: %w", d.NSU, db.Map(err))
	}
	docID, err := parseDocID(id)
	return docID, false, err
}

// ApplySummary records a resNFe. A new note starts as a summary, or as
// cancelled or denied when the summary says so. Its Ciência is pending when
// auto is on, the note is valid and it was issued at or after cutoff;
// otherwise it does not apply.
//
// A note that is already complete keeps its data and only gains the link to
// the summary. Cancelled wins over everything, then denied; a note that
// becomes cancelled or denied no longer needs its pending Ciência.
func (s *Store) ApplySummary(ctx context.Context, tx account.LockedTx, accountID, docID uuid.UUID, n dfe.Note, cutoff time.Time, auto bool) error {
	status := StatusSummary
	switch n.Situation {
	case dfe.Cancelled:
		status = StatusCancelled
	case dfe.Denied:
		status = StatusDenied
	}
	ciencia := CienciaNotApplicable
	if auto && status == StatusSummary && !n.IssuedAt.Before(cutoff) {
		ciencia = CienciaPending
	}

	const q = `
	INSERT INTO notes
		(id, account_id, key, emit_cnpj, emit_name, total_cents, issued_at, status, summary_doc_id, ciencia, seq)
	VALUES
		($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, nextval('note_seq'))
	ON CONFLICT (account_id, key) DO UPDATE SET
		summary_doc_id = COALESCE(notes.summary_doc_id, EXCLUDED.summary_doc_id),
		emit_cnpj   = CASE WHEN notes.full_doc_id IS NULL THEN EXCLUDED.emit_cnpj   ELSE notes.emit_cnpj   END,
		emit_name   = CASE WHEN notes.full_doc_id IS NULL THEN EXCLUDED.emit_name   ELSE notes.emit_name   END,
		total_cents = CASE WHEN notes.full_doc_id IS NULL THEN EXCLUDED.total_cents ELSE notes.total_cents END,
		issued_at   = CASE WHEN notes.full_doc_id IS NULL THEN EXCLUDED.issued_at   ELSE notes.issued_at   END,
		status = CASE
			WHEN notes.status = 'cancelada' OR EXCLUDED.status = 'cancelada' THEN 'cancelada'
			WHEN notes.status = 'denegada' OR EXCLUDED.status = 'denegada' THEN 'denegada'
			WHEN notes.status = 'completa' THEN 'completa'
			ELSE 'resumo'
		END,
		ciencia = CASE
			WHEN EXCLUDED.status IN ('cancelada', 'denegada') AND notes.ciencia = 'pendente' THEN 'nao_aplica'
			WHEN notes.summary_doc_id IS NULL AND notes.full_doc_id IS NULL
				AND notes.status = 'resumo' AND notes.ciencia = 'nao_aplica'
			THEN EXCLUDED.ciencia
			ELSE notes.ciencia
		END,
		seq = nextval('note_seq')`

	_, err := tx.ExecContext(ctx, q, uuid.NewV7().String(), accountID.String(), n.Key, n.EmitCNPJ, n.EmitName,
		n.TotalCents, n.IssuedAt, string(status), docID.String(), string(ciencia))
	if err != nil {
		return fmt.Errorf("applying summary %s: %w", n.Key, db.Map(err))
	}
	return nil
}

// ApplyFull records a procNFe: the note becomes complete, or denied when its
// protocol says so, unless it was cancelled, and takes its data from the
// full XML. A pending Ciência is no longer needed, since the XML it would
// release already came.
func (s *Store) ApplyFull(ctx context.Context, tx account.LockedTx, accountID, docID uuid.UUID, n dfe.Note) error {
	status := StatusFull
	if n.Situation == dfe.Denied {
		status = StatusDenied
	}

	const q = `
	INSERT INTO notes
		(id, account_id, key, emit_cnpj, emit_name, total_cents, issued_at, status, full_doc_id, ciencia, seq)
	VALUES
		($1, $2, $3, $4, $5, $6, $7, $9, $8, 'nao_aplica', nextval('note_seq'))
	ON CONFLICT (account_id, key) DO UPDATE SET
		emit_cnpj   = EXCLUDED.emit_cnpj,
		emit_name   = EXCLUDED.emit_name,
		total_cents = EXCLUDED.total_cents,
		issued_at   = EXCLUDED.issued_at,
		full_doc_id = EXCLUDED.full_doc_id,
		status  = CASE WHEN notes.status IN ('cancelada', 'denegada') THEN notes.status ELSE EXCLUDED.status END,
		ciencia = CASE WHEN notes.ciencia = 'pendente' THEN 'nao_aplica' ELSE notes.ciencia END,
		ciencia_next_at = NULL,
		seq = nextval('note_seq')`

	_, err := tx.ExecContext(ctx, q, uuid.NewV7().String(), accountID.String(), n.Key, n.EmitCNPJ, n.EmitName,
		n.TotalCents, n.IssuedAt, docID.String(), string(status))
	if err != nil {
		return fmt.Errorf("applying full note %s: %w", n.Key, db.Map(err))
	}
	return nil
}

// NewEvent is an event to attach to a note. DocID is the document it came
// in, when it came from SEFAZ; XML is kept for the events folio sends.
type NewEvent struct {
	Event  dfe.Event
	Origin string
	CStat  string
	DocID  *uuid.UUID
	XML    []byte
}

// ApplyEvent attaches an event to its note, creating the note when the event
// arrives first. A cancellation (110111) makes the note cancelled. The same
// event twice is stored once.
func (s *Store) ApplyEvent(ctx context.Context, tx account.LockedTx, accountID uuid.UUID, ne NewEvent) error {
	e := ne.Event
	cancel := e.Type == dfe.EventCancel

	status := StatusSummary
	if cancel {
		status = StatusCancelled
	}

	const qn = `
	INSERT INTO notes (id, account_id, key, status, ciencia, seq)
	VALUES ($1, $2, $3, $4, 'nao_aplica', nextval('note_seq'))
	ON CONFLICT (account_id, key) DO UPDATE SET
		status  = CASE WHEN $5 THEN 'cancelada' ELSE notes.status END,
		ciencia = CASE WHEN $5 AND notes.ciencia = 'pendente' THEN 'nao_aplica' ELSE notes.ciencia END,
		seq = nextval('note_seq')
	RETURNING id`

	var noteID string
	if err := tx.GetContext(ctx, &noteID, qn, uuid.NewV7().String(), accountID.String(), e.Key, string(status), cancel); err != nil {
		return fmt.Errorf("applying event to note %s: %w", e.Key, db.Map(err))
	}

	var docID *string
	if ne.DocID != nil {
		id := ne.DocID.String()
		docID = &id
	}

	const qe = `
	INSERT INTO note_events (id, note_id, type, seq, description, at, origin, cstat, doc_id, xml)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (note_id, type, seq, origin) DO NOTHING`

	_, err := tx.ExecContext(ctx, qe, uuid.NewV7().String(), noteID, e.Type, e.Seq, e.Description, e.At,
		ne.Origin, ne.CStat, docID, ne.XML)
	if err != nil {
		return fmt.Errorf("storing event %s of %s: %w", e.Type, e.Key, db.Map(err))
	}
	return nil
}

// NotesAfter returns up to limit notes of the account that changed after the
// cursor after, oldest change first.
func (s *Store) NotesAfter(ctx context.Context, accountID uuid.UUID, after int64, limit int) ([]Note, error) {
	limit = min(max(limit, 1), MaxPage)

	const q = `SELECT ` + noteColumns + ` FROM notes
	WHERE account_id = $1 AND seq > $2
	ORDER BY seq
	LIMIT $3`

	var rows []dbNote
	if err := s.db.SelectContext(ctx, &rows, q, accountID.String(), after, limit); err != nil {
		return nil, fmt.Errorf("querying notes: %w", db.Map(err))
	}
	return toNotes(rows)
}

// ForClient returns the note when its account is linked to clientID, and
// ErrNotFound otherwise.
func (s *Store) ForClient(ctx context.Context, clientID, noteID uuid.UUID) (Note, error) {
	const q = `SELECT ` + noteColumns + ` FROM notes
	WHERE id = $1 AND account_id IN (SELECT account_id FROM client_accounts WHERE client_id = $2)`

	var row dbNote
	if err := s.db.GetContext(ctx, &row, q, noteID.String(), clientID.String()); err != nil {
		return Note{}, fmt.Errorf("querying note: %w", mapErr(err))
	}
	return row.toNote()
}

// XML returns the best XML of a note, the full one when it came and the
// summary otherwise, with its kind.
func (s *Store) XML(ctx context.Context, noteID uuid.UUID) ([]byte, dfe.Kind, error) {
	const q = `
	SELECT d.xml, d.schema FROM notes n
	JOIN dfe_docs d ON d.id = COALESCE(n.full_doc_id, n.summary_doc_id)
	WHERE n.id = $1`

	var row struct {
		XML    []byte `db:"xml"`
		Schema string `db:"schema"`
	}
	if err := s.db.GetContext(ctx, &row, q, noteID.String()); err != nil {
		err = mapErr(err)
		if errors.Is(err, ErrNotFound) {
			err = ErrNoXML
		}
		return nil, "", fmt.Errorf("querying note xml: %w", err)
	}

	kind, err := dfe.KindOf(row.Schema)
	if err != nil {
		return nil, "", err
	}
	return row.XML, kind, nil
}

// Events returns the events of a note, oldest first.
func (s *Store) Events(ctx context.Context, noteID uuid.UUID) ([]Event, error) {
	const q = `
	SELECT type, seq, description, at, origin, cstat FROM note_events
	WHERE note_id = $1
	ORDER BY at, type, seq`

	var rows []struct {
		Type        string    `db:"type"`
		Seq         int       `db:"seq"`
		Description string    `db:"description"`
		At          time.Time `db:"at"`
		Origin      string    `db:"origin"`
		CStat       string    `db:"cstat"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, noteID.String()); err != nil {
		return nil, fmt.Errorf("querying events: %w", db.Map(err))
	}

	events := make([]Event, 0, len(rows))
	for _, r := range rows {
		events = append(events, Event(r))
	}
	return events, nil
}

// =============================================================================

const noteColumns = `id, account_id, key, emit_cnpj, emit_name, total_cents, issued_at, status,
	ciencia, ciencia_attempts, ciencia_next_at, seq, full_doc_id IS NOT NULL AS has_full`

type dbNote struct {
	ID              string       `db:"id"`
	AccountID       string       `db:"account_id"`
	Key             string       `db:"key"`
	EmitCNPJ        string       `db:"emit_cnpj"`
	EmitName        string       `db:"emit_name"`
	TotalCents      int64        `db:"total_cents"`
	IssuedAt        sql.NullTime `db:"issued_at"`
	Status          string       `db:"status"`
	Ciencia         string       `db:"ciencia"`
	CienciaAttempts int          `db:"ciencia_attempts"`
	CienciaNextAt   sql.NullTime `db:"ciencia_next_at"`
	Seq             int64        `db:"seq"`
	HasFull         bool         `db:"has_full"`
}

func (row dbNote) toNote() (Note, error) {
	id, err := uuid.Parse(row.ID)
	if err != nil {
		return Note{}, fmt.Errorf("parsing note id %q: %w", row.ID, err)
	}
	accountID, err := uuid.Parse(row.AccountID)
	if err != nil {
		return Note{}, fmt.Errorf("parsing account id %q: %w", row.AccountID, err)
	}

	n := Note{
		ID:              id,
		AccountID:       accountID,
		Key:             row.Key,
		EmitCNPJ:        row.EmitCNPJ,
		EmitName:        row.EmitName,
		TotalCents:      row.TotalCents,
		Status:          Status(row.Status),
		Ciencia:         Ciencia(row.Ciencia),
		CienciaAttempts: row.CienciaAttempts,
		Seq:             row.Seq,
		HasFull:         row.HasFull,
	}
	if row.IssuedAt.Valid {
		t := row.IssuedAt.Time
		n.IssuedAt = &t
	}
	if row.CienciaNextAt.Valid {
		t := row.CienciaNextAt.Time
		n.CienciaNextAt = &t
	}
	return n, nil
}

func toNotes(rows []dbNote) ([]Note, error) {
	notes := make([]Note, 0, len(rows))
	for _, row := range rows {
		n, err := row.toNote()
		if err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, nil
}

func parseDocID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("parsing doc id %q: %w", s, err)
	}
	return id, nil
}

func mapErr(err error) error {
	err = db.Map(err)
	if errors.Is(err, db.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
