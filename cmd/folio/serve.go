package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/apiclient"
	"github.com/guiyones/folio/internal/httpapi"
	"github.com/guiyones/folio/internal/note"
	"github.com/guiyones/folio/internal/platform/crypt"
	"github.com/guiyones/folio/internal/platform/db"
	"github.com/guiyones/folio/internal/scan"
)

// serve runs the API and the scan until ctx is done.
func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	noScan := fs.Bool("no-scan", false, "sobe só a API, sem a varredura")
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	url := os.Getenv("FOLIO_DB_URL")
	if url == "" {
		return errors.New("FOLIO_DB_URL não definida")
	}
	addr := os.Getenv("FOLIO_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	box, err := crypt.FromEnv()
	if err != nil {
		return err
	}
	roots, err := sefazRoots()
	if err != nil {
		return err
	}

	conn, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close() // closing a pool at exit only fails on connections already gone
	if err := db.Migrate(ctx, conn); err != nil {
		return err
	}

	accounts := account.NewStore(conn)
	notes := note.NewStore(conn)

	// The scan stops with ctx; serve waits for it before closing the
	// database, so a run in progress can record what it got. Defers run in
	// reverse, so cancel stops the scan before Wait: serve returning for any
	// reason, such as a busy port, ends the scan too.
	ctx, stopScan := context.WithCancel(ctx)
	var scanning sync.WaitGroup
	defer scanning.Wait()
	defer stopScan()

	if !*noScan {
		scanner := &scan.Scanner{
			DB:       conn,
			Accounts: accounts,
			Notes:    notes,
			Box:      box,
			Dial: func(cert *dfe.Certificate, env dfe.Env) (scan.SEFAZ, error) {
				return dfe.NewClient(cert, env, dfe.WithRootCAs(roots))
			},
			Now: func() time.Time { return time.Now().UTC() },
			Log: log.With("part", "scan"),
		}
		scanning.Go(func() { scanner.Loop(ctx, scan.Tick) })
	}

	srv := &http.Server{
		Addr: addr,
		Handler: httpapi.New(httpapi.Deps{
			DB:       conn,
			Accounts: accounts,
			Notes:    notes,
			Clients:  apiclient.NewStore(conn),
			Box:      box,
			Now:      func() time.Time { return time.Now().UTC() },
			Log:      log.With("part", "api"),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		errs <- srv.ListenAndServe()
	}()

	select {
	case err := <-errs:
		return fmt.Errorf("api: %w", err)
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return fmt.Errorf("stopping the api: %w", err)
	}
	return nil
}
