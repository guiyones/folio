// Package dbtest gives each test its own migrated database.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/guiyones/folio/internal/platform/db"
	"github.com/jmoiron/sqlx"
)

// New creates an empty database next to FOLIO_TEST_DB_URL, migrates it and
// drops it when the test finishes. The test is skipped when the variable is
// not set; `make test` sets it for the compose database.
func New(t *testing.T) *sqlx.DB {
	t.Helper()
	db, _ := NewWithURL(t) // the URL is only for tests that open their own connection
	return db
}

// NewWithURL is New that also returns the URL of the test database, for
// code that opens its own connection, like the admin command.
func NewWithURL(t *testing.T) (*sqlx.DB, string) {
	t.Helper()

	admin := os.Getenv("FOLIO_TEST_DB_URL")
	if admin == "" {
		t.Skip("FOLIO_TEST_DB_URL não definida")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbA, err := db.Open(ctx, admin)
	if err != nil {
		t.Fatalf("opening admin database: %v", err)
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	name := "test_" + hex.EncodeToString(suffix)

	if _, err := dbA.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating database %s: %v", name, err)
	}

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("parsing FOLIO_TEST_DB_URL: %v", err)
	}
	u.Path = "/" + name

	dbT, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("opening database %s: %v", name, err)
	}

	t.Cleanup(func() {
		if err := dbT.Close(); err != nil {
			t.Errorf("closing database %s: %v", name, err)
		}
		if _, err := dbA.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping database %s: %v", name, err)
		}
		if err := dbA.Close(); err != nil {
			t.Errorf("closing admin database: %v", err)
		}
	})

	if err := db.Migrate(ctx, dbT); err != nil {
		t.Fatalf("migrating database %s: %v", name, err)
	}

	return dbT, u.String()
}
