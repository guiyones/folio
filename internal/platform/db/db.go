// Package db opens the Postgres connection, runs the migrations and maps
// database errors to the ones the domains translate.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

// Errors the domain stores translate into their own.
var (
	ErrNotFound   = errors.New("not found")
	ErrDuplicate  = errors.New("duplicated entry")
	ErrForeignKey = errors.New("foreign key violation")
)

// DuplicateError is a unique violation, naming the constraint that refused
// the write. It matches ErrDuplicate with errors.Is.
type DuplicateError struct {
	Constraint string
}

// Error implements the error interface.
func (e *DuplicateError) Error() string {
	return ErrDuplicate.Error() + ": " + e.Constraint
}

// Is makes errors.Is(err, ErrDuplicate) true.
func (e *DuplicateError) Is(target error) bool {
	return target == ErrDuplicate
}

// ForeignKeyError is a foreign key violation, naming the constraint. It
// matches ErrForeignKey with errors.Is.
type ForeignKeyError struct {
	Constraint string
}

// Error implements the error interface.
func (e *ForeignKeyError) Error() string {
	return ErrForeignKey.Error() + ": " + e.Constraint
}

// Is makes errors.Is(err, ErrForeignKey) true.
func (e *ForeignKeyError) Is(target error) bool {
	return target == ErrForeignKey
}

// Open connects to the database at url (postgres://...) and checks that it
// answers.
func Open(ctx context.Context, url string) (*sqlx.DB, error) {
	db, err := sqlx.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := StatusCheck(ctx, db); err != nil {
		if errClose := db.Close(); errClose != nil {
			return nil, errors.Join(err, fmt.Errorf("closing database: %w", errClose))
		}
		return nil, err
	}

	return db, nil
}

// StatusCheck pings the database until it answers or ctx is done, giving a
// freshly started container time to come up.
func StatusCheck(ctx context.Context, db *sqlx.DB) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	for {
		err := db.PingContext(ctx)
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("database not answering: %w", errors.Join(err, ctx.Err()))
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Map converts driver errors into ErrNotFound, a *DuplicateError or a
// *ForeignKeyError, and returns any other error unchanged.
func Map(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case uniqueViolation:
			return &DuplicateError{Constraint: pgErr.ConstraintName}
		case foreignKeyViolation:
			return &ForeignKeyError{Constraint: pgErr.ConstraintName}
		}
	}

	return err
}

// WithTx runs fn inside a transaction, committing when fn returns nil and
// rolling back otherwise.
func WithTx(ctx context.Context, db *sqlx.DB, fn func(tx *sqlx.Tx) error) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if errRb := tx.Rollback(); errRb != nil {
			return errors.Join(err, fmt.Errorf("rolling back: %w", errRb))
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// Rollback rolls tx back, ignoring a transaction that is already done. It is
// meant for a defer that runs after Commit.
func Rollback(tx *sqlx.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("rolling back: %w", err)
	}
	return nil
}
