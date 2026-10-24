package db

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/ardanlabs/darwin/v3"
	"github.com/ardanlabs/darwin/v3/dialects/postgres"
	"github.com/ardanlabs/darwin/v3/drivers/generic"
	"github.com/jmoiron/sqlx"
)

//go:embed sql/migrate.sql
var migrateDoc string

// Migrate brings the database up to date with the migrations in
// sql/migrate.sql.
func Migrate(ctx context.Context, db *sqlx.DB) error {
	if err := StatusCheck(ctx, db); err != nil {
		return fmt.Errorf("status check database: %w", err)
	}

	driver, err := generic.New(db.DB, postgres.Dialect{})
	if err != nil {
		return fmt.Errorf("constructing darwin driver: %w", err)
	}

	d := darwin.New(driver, darwin.ParseMigrations(migrateDoc))
	if err := d.Migrate(); err != nil {
		return fmt.Errorf("migrating: %w", err)
	}
	return nil
}
