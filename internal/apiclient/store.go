package apiclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/guiyones/folio/internal/platform/db"
	"github.com/jmoiron/sqlx"
)

// prefixLen is how much of a key is stored in clear, to name it when
// revoking.
const prefixLen = 12

// Store keeps API clients and keys in Postgres. Keys are stored only as a
// SHA-256 hash: they are long random strings, so a slow hash adds nothing.
type Store struct {
	db *sqlx.DB
}

// NewStore returns a Store over db.
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db}
}

// Create registers an API client.
func (s *Store) Create(ctx context.Context, name string, now time.Time) (Client, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Client{}, fmt.Errorf("%w: o nome é obrigatório", ErrInvalid)
	}

	c := Client{ID: uuid.NewV7(), Name: name, CreatedAt: now}

	const q = `INSERT INTO clients (id, name, created_at) VALUES ($1, $2, $3)`
	if _, err := s.db.ExecContext(ctx, q, c.ID.String(), c.Name, c.CreatedAt); err != nil {
		return Client{}, fmt.Errorf("creating client: %w", db.Map(err))
	}
	return c, nil
}

// ByID returns the client with id.
func (s *Store) ByID(ctx context.Context, id uuid.UUID) (Client, error) {
	const q = `SELECT id, name, created_at FROM clients WHERE id = $1`

	var row dbClient
	if err := s.db.GetContext(ctx, &row, q, id.String()); err != nil {
		return Client{}, fmt.Errorf("querying client: %w", mapErr(err))
	}
	return row.toClient()
}

// CreateKey makes a new API key for clientID and returns it. The key is not
// stored and cannot be shown again.
func (s *Store) CreateKey(ctx context.Context, clientID uuid.UUID, now time.Time) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generating key: %w", err)
	}
	plain := KeyPrefix + base64.RawURLEncoding.EncodeToString(random)
	hash := sha256.Sum256([]byte(plain))

	const q = `
	INSERT INTO api_keys (id, client_id, hash, prefix, created_at)
	VALUES ($1, $2, $3, $4, $5)`

	if _, err := s.db.ExecContext(ctx, q, uuid.NewV7().String(), clientID.String(), hash[:], plain[:prefixLen], now); err != nil {
		return "", fmt.Errorf("creating key: %w", mapErr(err))
	}
	return plain, nil
}

// ByKey returns the client that owns the API key plain. An unknown or
// revoked key gives ErrNotFound.
func (s *Store) ByKey(ctx context.Context, plain string) (Client, error) {
	if !strings.HasPrefix(plain, KeyPrefix) {
		return Client{}, ErrNotFound
	}
	hash := sha256.Sum256([]byte(plain))

	const q = `
	SELECT c.id, c.name, c.created_at
	FROM api_keys k JOIN clients c ON c.id = k.client_id
	WHERE k.hash = $1 AND k.revoked_at IS NULL`

	var row dbClient
	if err := s.db.GetContext(ctx, &row, q, hash[:]); err != nil {
		return Client{}, fmt.Errorf("querying key: %w", mapErr(err))
	}
	return row.toClient()
}

// RevokeKey revokes the key whose first characters are prefix, as printed
// when it was created.
func (s *Store) RevokeKey(ctx context.Context, prefix string, now time.Time) error {
	const q = `UPDATE api_keys SET revoked_at = $2 WHERE prefix = $1 AND revoked_at IS NULL`

	res, err := s.db.ExecContext(ctx, q, prefix, now)
	if err != nil {
		return fmt.Errorf("revoking key: %w", mapErr(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("chave %q não encontrada ou já revogada: %w", prefix, ErrNotFound)
	}
	return nil
}

// Prefix returns the part of a key that is stored in clear.
func Prefix(plain string) string {
	if len(plain) < prefixLen {
		return plain
	}
	return plain[:prefixLen]
}

type dbClient struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	CreatedAt time.Time `db:"created_at"`
}

func (row dbClient) toClient() (Client, error) {
	id, err := uuid.Parse(row.ID)
	if err != nil {
		return Client{}, fmt.Errorf("parsing client id %q: %w", row.ID, err)
	}
	return Client{ID: id, Name: row.Name, CreatedAt: row.CreatedAt}, nil
}

func mapErr(err error) error {
	err = db.Map(err)
	if errors.Is(err, db.ErrNotFound) || errors.Is(err, db.ErrForeignKey) {
		return ErrNotFound
	}
	return err
}
