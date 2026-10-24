package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/platform/db"
	"github.com/jmoiron/sqlx"
)

var cnpjRE = regexp.MustCompile(`^[0-9]{14}$`)

// Store keeps accounts and certificates in Postgres.
type Store struct {
	db *sqlx.DB
}

// NewStore returns a Store over db.
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db}
}

// Create registers an account linked to na.ClientID. The account starts
// with automatic Ciência on, the Ciência cutoff at now and a scan due now.
func (s *Store) Create(ctx context.Context, na NewAccount, now time.Time) (Account, error) {
	if err := validate(na); err != nil {
		return Account{}, err
	}

	a := Account{
		ID:            uuid.NewV7(),
		Name:          na.Name,
		CNPJ:          na.CNPJ,
		UF:            na.UF,
		Env:           na.Env,
		UltNSU:        na.StartNSU,
		NextScanAt:    now,
		AutoCiencia:   true,
		CienciaCutoff: now,
		CreatedAt:     now,
	}

	const qa = `
	INSERT INTO accounts
		(id, name, cnpj, uf, env, ult_nsu, next_scan_at, auto_ciencia, ciencia_cutoff, created_at)
	VALUES
		(:id, :name, :cnpj, :uf, :env, :ult_nsu, :next_scan_at, :auto_ciencia, :ciencia_cutoff, :created_at)`

	const ql = `INSERT INTO client_accounts (client_id, account_id) VALUES ($1, $2)`

	err := db.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
		if _, err := tx.NamedExecContext(ctx, qa, toDB(a)); err != nil {
			return mapErr(err)
		}
		if _, err := tx.ExecContext(ctx, ql, na.ClientID.String(), a.ID.String()); err != nil {
			return mapErr(err)
		}
		return nil
	})
	if err != nil {
		return Account{}, fmt.Errorf("creating account: %w", err)
	}

	return a, nil
}

// ByID returns the account with id.
func (s *Store) ByID(ctx context.Context, id uuid.UUID) (Account, error) {
	const q = `SELECT ` + columns + ` FROM accounts WHERE id = $1`

	var row dbAccount
	if err := s.db.GetContext(ctx, &row, q, id.String()); err != nil {
		return Account{}, fmt.Errorf("querying account: %w", mapErr(err))
	}
	return row.toAccount()
}

// ForClient returns the account with id when it is linked to clientID, and
// ErrNotFound otherwise, so a client never learns about other accounts.
func (s *Store) ForClient(ctx context.Context, clientID, id uuid.UUID) (Account, error) {
	const q = `
	SELECT ` + columns + ` FROM accounts
	WHERE id = $1 AND id IN (SELECT account_id FROM client_accounts WHERE client_id = $2)`

	var row dbAccount
	if err := s.db.GetContext(ctx, &row, q, id.String(), clientID.String()); err != nil {
		return Account{}, fmt.Errorf("querying account: %w", mapErr(err))
	}
	return row.toAccount()
}

// ListForClient returns the accounts linked to clientID, by name.
func (s *Store) ListForClient(ctx context.Context, clientID uuid.UUID) ([]Account, error) {
	const q = `
	SELECT ` + columns + ` FROM accounts
	WHERE id IN (SELECT account_id FROM client_accounts WHERE client_id = $1)
	ORDER BY name, id`

	var rows []dbAccount
	if err := s.db.SelectContext(ctx, &rows, q, clientID.String()); err != nil {
		return nil, fmt.Errorf("querying accounts: %w", mapErr(err))
	}

	accounts := make([]Account, 0, len(rows))
	for _, row := range rows {
		a, err := row.toAccount()
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}

// LinkClient gives clientID access to the account. Linking twice is a no-op.
func (s *Store) LinkClient(ctx context.Context, accountID, clientID uuid.UUID) error {
	const q = `
	INSERT INTO client_accounts (client_id, account_id) VALUES ($1, $2)
	ON CONFLICT DO NOTHING`

	if _, err := s.db.ExecContext(ctx, q, clientID.String(), accountID.String()); err != nil {
		return fmt.Errorf("linking client: %w", mapErr(err))
	}
	return nil
}

// SetPaused pauses or resumes the account. A paused account is not scanned
// and sends no Ciência, but its notes stay readable.
func (s *Store) SetPaused(ctx context.Context, id uuid.UUID, paused bool) error {
	const q = `UPDATE accounts SET paused = $2 WHERE id = $1`

	res, err := s.db.ExecContext(ctx, q, id.String(), paused)
	if err != nil {
		return fmt.Errorf("setting paused: %w", mapErr(err))
	}
	return mustAffect(res, ErrNotFound)
}

// PutCertificate stores c as the certificate of the account, replacing the
// previous one.
func (s *Store) PutCertificate(ctx context.Context, accountID uuid.UUID, c StoredCert) error {
	const q = `
	INSERT INTO certificates
		(account_id, holder, cnpj, not_after, key_id, pfx, password, uploaded_at)
	VALUES
		($1, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT (account_id) DO UPDATE SET
		holder = EXCLUDED.holder,
		cnpj = EXCLUDED.cnpj,
		not_after = EXCLUDED.not_after,
		key_id = EXCLUDED.key_id,
		pfx = EXCLUDED.pfx,
		password = EXCLUDED.password,
		uploaded_at = EXCLUDED.uploaded_at`

	_, err := s.db.ExecContext(ctx, q, accountID.String(), c.Holder, c.CNPJ, c.NotAfter, c.KeyID, c.PFX, c.Password, c.UploadedAt)
	if err != nil {
		return fmt.Errorf("storing certificate: %w", mapErr(err))
	}
	return nil
}

// Certificate returns the stored certificate of the account, or
// ErrNoCertificate.
func (s *Store) Certificate(ctx context.Context, accountID uuid.UUID) (StoredCert, error) {
	const q = `
	SELECT holder, cnpj, not_after, key_id, pfx, password, uploaded_at
	FROM certificates WHERE account_id = $1`

	var row dbCert
	if err := s.db.GetContext(ctx, &row, q, accountID.String()); err != nil {
		err = mapErr(err)
		if errors.Is(err, ErrNotFound) {
			err = ErrNoCertificate
		}
		return StoredCert{}, fmt.Errorf("querying certificate: %w", err)
	}

	return StoredCert{
		Holder:     row.Holder,
		CNPJ:       row.CNPJ,
		NotAfter:   row.NotAfter,
		UploadedAt: row.UploadedAt,
		KeyID:      row.KeyID,
		PFX:        row.PFX,
		Password:   row.Password,
	}, nil
}

// DeleteCertificate removes the certificate of the account. The scan stops
// until a new one is uploaded.
func (s *Store) DeleteCertificate(ctx context.Context, accountID uuid.UUID) error {
	const q = `DELETE FROM certificates WHERE account_id = $1`

	res, err := s.db.ExecContext(ctx, q, accountID.String())
	if err != nil {
		return fmt.Errorf("deleting certificate: %w", mapErr(err))
	}
	return mustAffect(res, ErrNoCertificate)
}

// =============================================================================

const columns = `id, name, cnpj, uf, env, ult_nsu, next_scan_at, last_scan_at, last_cstat,
	last_message, paused, auto_ciencia, ciencia_cutoff, created_at`

type dbAccount struct {
	ID            string       `db:"id"`
	Name          string       `db:"name"`
	CNPJ          string       `db:"cnpj"`
	UF            int          `db:"uf"`
	Env           int          `db:"env"`
	UltNSU        int64        `db:"ult_nsu"`
	NextScanAt    time.Time    `db:"next_scan_at"`
	LastScanAt    sql.NullTime `db:"last_scan_at"`
	LastCStat     string       `db:"last_cstat"`
	LastMessage   string       `db:"last_message"`
	Paused        bool         `db:"paused"`
	AutoCiencia   bool         `db:"auto_ciencia"`
	CienciaCutoff time.Time    `db:"ciencia_cutoff"`
	CreatedAt     time.Time    `db:"created_at"`
}

func toDB(a Account) dbAccount {
	row := dbAccount{
		ID:            a.ID.String(),
		Name:          a.Name,
		CNPJ:          a.CNPJ,
		UF:            a.UF,
		Env:           int(a.Env),
		UltNSU:        a.UltNSU,
		NextScanAt:    a.NextScanAt,
		LastCStat:     a.LastCStat,
		LastMessage:   a.LastMessage,
		Paused:        a.Paused,
		AutoCiencia:   a.AutoCiencia,
		CienciaCutoff: a.CienciaCutoff,
		CreatedAt:     a.CreatedAt,
	}
	if a.LastScanAt != nil {
		row.LastScanAt = sql.NullTime{Time: *a.LastScanAt, Valid: true}
	}
	return row
}

func (row dbAccount) toAccount() (Account, error) {
	id, err := uuid.Parse(row.ID)
	if err != nil {
		return Account{}, fmt.Errorf("parsing account id %q: %w", row.ID, err)
	}

	a := Account{
		ID:            id,
		Name:          row.Name,
		CNPJ:          row.CNPJ,
		UF:            row.UF,
		Env:           dfe.Env(row.Env),
		UltNSU:        row.UltNSU,
		NextScanAt:    row.NextScanAt,
		LastCStat:     row.LastCStat,
		LastMessage:   row.LastMessage,
		Paused:        row.Paused,
		AutoCiencia:   row.AutoCiencia,
		CienciaCutoff: row.CienciaCutoff,
		CreatedAt:     row.CreatedAt,
	}
	if row.LastScanAt.Valid {
		t := row.LastScanAt.Time
		a.LastScanAt = &t
	}
	return a, nil
}

type dbCert struct {
	Holder     string    `db:"holder"`
	CNPJ       string    `db:"cnpj"`
	NotAfter   time.Time `db:"not_after"`
	KeyID      string    `db:"key_id"`
	PFX        []byte    `db:"pfx"`
	Password   []byte    `db:"password"`
	UploadedAt time.Time `db:"uploaded_at"`
}

func validate(na NewAccount) error {
	switch {
	case na.Name == "":
		return fmt.Errorf("%w: o nome é obrigatório", ErrInvalid)
	case !cnpjRE.MatchString(na.CNPJ):
		return fmt.Errorf("%w: o CNPJ precisa ter 14 dígitos", ErrInvalid)
	case na.UF < 11 || na.UF > 53:
		return fmt.Errorf("%w: código de UF %d inválido", ErrInvalid, na.UF)
	case na.Env != dfe.Production && na.Env != dfe.Homologation:
		return fmt.Errorf("%w: ambiente %d inválido", ErrInvalid, na.Env)
	case na.StartNSU < 0:
		return fmt.Errorf("%w: o NSU inicial não pode ser negativo", ErrInvalid)
	}
	return nil
}

// mapErr translates database errors into the account ones.
func mapErr(err error) error {
	err = db.Map(err)
	switch {
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, db.ErrDuplicate):
		return ErrDuplicate
	}

	var fk *db.ForeignKeyError
	if errors.As(err, &fk) {
		if strings.Contains(fk.Constraint, "client_id") {
			return ErrUnknownClient
		}
		return ErrNotFound
	}
	return err
}

func mustAffect(res sql.Result, none error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return none
	}
	return nil
}
