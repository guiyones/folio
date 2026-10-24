// Package scan runs the NFeDistribuicaoDFe scan of every account: it asks
// SEFAZ for new documents within the one-hour limit, stores them as notes
// and sends the pending Ciência da Operação.
package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/note"
	"github.com/guiyones/folio/internal/platform/crypt"
	"github.com/guiyones/folio/internal/platform/db"
	"github.com/jmoiron/sqlx"
)

// Limits of the scan.
const (
	// Wait is how long an account waits after a 137, a 656, an error or
	// reaching maxNSU. SEFAZ blocks a CNPJ that asks more often.
	Wait = time.Hour

	// MaxPages bounds the calls of one run; an account with more to fetch
	// is picked again right away.
	MaxPages = 20

	// CertWarn is how long before expiry the certificate starts warning.
	CertWarn = 30 * 24 * time.Hour

	// Tick is how often Loop looks for due accounts.
	Tick = time.Minute

	// CienciaPerRun bounds the Ciência sent in one run.
	CienciaPerRun = 20

	// CienciaRetry is how long a failed Ciência waits to be sent again.
	CienciaRetry = time.Hour
)

// SEFAZ is what the scan needs from dfe.Client.
type SEFAZ interface {
	DistNSU(ctx context.Context, uf int, cnpj string, ultNSU int64) (dfe.DistResult, error)
	Ciencia(ctx context.Context, cnpj, key string, at time.Time) (dfe.EventResult, error)
}

// Dialer builds a SEFAZ client for a certificate and environment.
type Dialer func(cert *dfe.Certificate, env dfe.Env) (SEFAZ, error)

// Scanner scans the accounts that are due.
type Scanner struct {
	Accounts *account.Store
	Notes    *note.Store
	Box      *crypt.Box
	Dial     Dialer
	Now      func() time.Time
	Log      *slog.Logger
}

// Loop scans due accounts every tick until ctx is done. Errors are logged and
// never stop the loop.
func (s *Scanner) Loop(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		for {
			ran, err := s.RunOnce(ctx)
			if err != nil {
				s.Log.Error("scan", "err", err)
			}
			if !ran || ctx.Err() != nil {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce scans one due account. ran is false when no account was due.
// SEFAZ answers that are not documents (137, 656, rejections, network
// errors) are recorded on the account and are not errors; err reports what
// went wrong with folio itself, like the database.
func (s *Scanner) RunOnce(ctx context.Context) (ran bool, err error) {
	now := s.Now()

	tx, a, found, err := s.Accounts.LockDue(ctx, now)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	defer func() {
		if errRb := db.Rollback(tx); errRb != nil {
			err = errors.Join(err, errRb)
		}
	}()

	log := s.Log.With("account", a.ID, "cnpj", a.CNPJ)

	// A failure partway still returns how far the scan got, so the pages
	// already stored are kept and the account waits before trying again.
	result, scanErr := s.scan(ctx, tx, a, now, log)
	if scanErr != nil {
		scanErr = fmt.Errorf("scanning account %s: %w", a.ID, scanErr)
	}
	if result.Next.IsZero() {
		return true, scanErr
	}

	if err := s.Accounts.FinishScan(ctx, tx, a.ID, result, now); err != nil {
		return true, errors.Join(scanErr, err)
	}
	if err := tx.Commit(); err != nil {
		return true, errors.Join(scanErr, fmt.Errorf("committing scan of %s: %w", a.ID, err))
	}

	log.Info("scan", "ult_nsu", result.UltNSU, "cstat", result.CStat, "next", result.Next)
	return true, scanErr
}

// scan does the work of one run inside tx and says how it ended.
func (s *Scanner) scan(ctx context.Context, tx *sqlx.Tx, a account.Account, now time.Time, log *slog.Logger) (account.ScanResult, error) {
	wait := account.ScanResult{UltNSU: a.UltNSU, Next: now.Add(Wait)}

	stored, err := s.Accounts.Certificate(ctx, a.ID)
	if err != nil {
		wait.Message = "erro ao ler o certificado"
		return wait, err
	}
	if !stored.NotAfter.After(now) {
		wait.Message = "certificado vencido"
		log.Warn("certificate expired", "not_after", stored.NotAfter)
		return wait, nil
	}
	if stored.NotAfter.Sub(now) <= CertWarn {
		log.Warn("certificate expires soon", "not_after", stored.NotAfter)
	}

	cert, err := account.OpenCertificate(stored, s.Box)
	if err != nil {
		wait.Message = err.Error()
		log.Error("opening certificate", "err", err)
		return wait, nil
	}
	sefaz, err := s.Dial(cert, a.Env)
	if err != nil {
		wait.Message = err.Error()
		log.Error("building sefaz client", "err", err)
		return wait, nil
	}

	result, err := s.pages(ctx, tx, sefaz, a, now, log)
	if err != nil {
		return result, err
	}

	if err := savepoint(ctx, tx, "ciencia", func() error { return s.ciencia(ctx, tx, sefaz, a, now, log) }); err != nil {
		return result, err
	}

	return result, nil
}

// savepoint runs fn under a savepoint and rolls back to it when fn fails,
// leaving tx usable for what was done before.
func savepoint(ctx context.Context, tx *sqlx.Tx, name string, fn func() error) error {
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return fmt.Errorf("savepoint %s: %w", name, err)
	}

	if err := fn(); err != nil {
		if _, errRb := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+name); errRb != nil {
			return errors.Join(err, fmt.Errorf("rolling back to %s: %w", name, errRb))
		}
		return err
	}

	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return fmt.Errorf("releasing savepoint %s: %w", name, err)
	}
	return nil
}

// pages calls NFeDistribuicaoDFe until there is nothing more, the limit
// says wait, or MaxPages calls were made. Each page is stored under a
// savepoint, so a failure keeps the pages before it and their NSU.
func (s *Scanner) pages(ctx context.Context, tx *sqlx.Tx, sefaz SEFAZ, a account.Account, now time.Time, log *slog.Logger) (account.ScanResult, error) {
	ult := a.UltNSU

	for range MaxPages {
		r, err := sefaz.DistNSU(ctx, a.UF, a.CNPJ, ult)
		if err != nil {
			log.Warn("distdfe", "err", err)
			return account.ScanResult{UltNSU: ult, Next: now.Add(Wait), Message: err.Error()}, nil
		}

		done := account.ScanResult{UltNSU: max(ult, r.UltNSU), Next: now.Add(Wait), CStat: r.CStat, Message: r.Motivo}

		switch r.CStat {
		case dfe.CStatNoDocs:
			return done, nil
		case dfe.CStatOveruse:
			log.Warn("sefaz says consumo indevido", "motivo", r.Motivo)
			return done, nil
		case dfe.CStatDocsFound:
		default:
			log.Warn("distdfe rejected", "cstat", r.CStat, "motivo", r.Motivo)
			done.UltNSU = ult
			return done, nil
		}

		if err := savepoint(ctx, tx, "page", func() error { return s.savePage(ctx, tx, a, r.Docs, now, log) }); err != nil {
			return account.ScanResult{UltNSU: ult, Next: now.Add(Wait), Message: "erro ao gravar os documentos"}, err
		}

		ult = done.UltNSU
		if ult >= r.MaxNSU {
			return done, nil
		}
	}

	// More to fetch: the next tick continues.
	return account.ScanResult{UltNSU: ult, Next: now, CStat: dfe.CStatDocsFound, Message: "continua na próxima rodada"}, nil
}

// savePage stores the documents of one answer and applies the new ones.
func (s *Scanner) savePage(ctx context.Context, tx *sqlx.Tx, a account.Account, docs []dfe.Doc, now time.Time, log *slog.Logger) error {
	for _, d := range docs {
		docID, isNew, err := s.Notes.SaveDoc(ctx, tx, a.ID, d, now)
		if err != nil {
			return err
		}
		if !isNew {
			continue
		}
		if err := s.apply(ctx, tx, a, docID, d, log); err != nil {
			return fmt.Errorf("NSU %d: %w", d.NSU, err)
		}
	}
	return nil
}

// apply turns a new document into a note or an event. A document folio
// cannot read stays stored and is only logged, so one odd document never
// stops the scan.
func (s *Scanner) apply(ctx context.Context, tx *sqlx.Tx, a account.Account, docID uuid.UUID, d dfe.Doc, log *slog.Logger) error {
	kind, err := dfe.KindOf(d.Schema)
	if err != nil {
		log.Warn("unknown document kept as is", "nsu", d.NSU, "schema", d.Schema)
		return nil
	}

	unreadable := func(err error) error {
		log.Warn("unreadable document kept as is", "nsu", d.NSU, "schema", d.Schema, "err", err)
		return nil
	}

	switch kind {
	case dfe.KindSummary:
		n, err := dfe.ParseSummary(d.XML)
		if err != nil {
			return unreadable(err)
		}
		return s.Notes.ApplySummary(ctx, tx, a.ID, docID, n, a.CienciaCutoff, a.AutoCiencia)

	case dfe.KindFull:
		n, err := dfe.ParseFull(d.XML)
		if err != nil {
			return unreadable(err)
		}
		return s.Notes.ApplyFull(ctx, tx, a.ID, docID, n)

	default:
		e, err := dfe.ParseEvent(kind, d.XML)
		if err != nil {
			return unreadable(err)
		}
		id := docID
		return s.Notes.ApplyEvent(ctx, tx, a.ID, note.NewEvent{Event: e, Origin: note.OriginSEFAZ, DocID: &id})
	}
}

// ciencia sends the due Ciência of the account. A refusal or a network
// error only schedules a retry; it never stops the scan.
func (s *Scanner) ciencia(ctx context.Context, tx *sqlx.Tx, sefaz SEFAZ, a account.Account, now time.Time, log *slog.Logger) error {
	due, err := s.Notes.CienciaDue(ctx, tx, a.ID, now, CienciaPerRun)
	if err != nil {
		return err
	}

	for _, n := range due {
		r, err := sefaz.Ciencia(ctx, a.CNPJ, n.Key, now)
		if err != nil {
			log.Warn("ciência", "key", n.Key, "err", err)
			if err := s.Notes.CienciaFailed(ctx, tx, n.ID, now.Add(CienciaRetry)); err != nil {
				return err
			}
			continue
		}

		event := note.NewEvent{
			Event:  dfe.Event{Key: n.Key, Type: dfe.EventCiencia, Seq: 1, Description: "Ciencia da Operacao", At: now},
			Origin: note.OriginFolio,
			CStat:  r.CStat,
			XML:    r.XML,
		}
		if err := s.Notes.ApplyEvent(ctx, tx, a.ID, event); err != nil {
			return err
		}
		if err := s.Notes.CienciaSent(ctx, tx, n.ID); err != nil {
			return err
		}
	}
	return nil
}
