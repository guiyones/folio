// Package scan runs the NFeDistribuicaoDFe scan of every account: it asks
// SEFAZ for new documents within the one-hour limit, stores them as notes
// and sends the pending Ciência da Operação.
//
// No transaction is open while folio talks to SEFAZ. A run first reserves
// the account's next hour (account.Store.Claim) and commits it, so whatever
// happens next (a crash, a deploy, a lost connection) the CNPJ is not
// queried again too soon. Each answer is then stored in its own short
// transaction that holds the account lock, together with the NSU it reached.
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
)

// Limits of the scan.
const (
	// Wait is how long an account waits after a 137, a 656, an error or
	// reaching maxNSU, counted from the SEFAZ answer. SEFAZ blocks a CNPJ
	// that asks more than once an hour; the extra minutes absorb clock skew
	// between folio and SEFAZ.
	Wait = time.Hour + 5*time.Minute

	// MaxPages bounds the calls of one run; an account with more to fetch
	// is picked again on the next tick.
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

// Loop scans due accounts every tick until ctx is done. Errors are logged
// and never stop the loop; after an error it waits for the next tick.
func (s *Scanner) Loop(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		for ctx.Err() == nil {
			ran, err := s.RunOnce(ctx)
			if err != nil {
				s.Log.Error("scan", "err", err)
				break
			}
			if !ran {
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

	a, found, err := s.Accounts.Claim(ctx, now, now.Add(Wait))
	if err != nil || !found {
		return false, err
	}
	log := s.Log.With("account", a.ID, "cnpj", a.CNPJ)

	sefaz, problem, err := s.connect(ctx, a, now, log)
	if err != nil {
		return true, fmt.Errorf("connecting account %s: %w", a.ID, err)
	}
	if problem != "" {
		return true, s.finish(ctx, a.ID, account.ScanResult{UltNSU: a.UltNSU, Next: now.Add(Wait), Message: problem}, now)
	}
	if c, ok := sefaz.(interface{ CloseIdleConnections() }); ok {
		defer c.CloseIdleConnections()
	}

	if err := s.pages(ctx, sefaz, a, log); err != nil {
		return true, fmt.Errorf("scanning account %s: %w", a.ID, err)
	}
	if err := s.ciencia(ctx, sefaz, a, log); err != nil {
		return true, fmt.Errorf("sending ciência of account %s: %w", a.ID, err)
	}
	return true, nil
}

// connect opens the certificate and builds the SEFAZ client. problem says
// why the account cannot be scanned now, like an expired certificate.
func (s *Scanner) connect(ctx context.Context, a account.Account, now time.Time, log *slog.Logger) (SEFAZ, string, error) {
	stored, err := s.Accounts.Certificate(ctx, a.ID)
	if err != nil {
		return nil, "", err
	}
	if !stored.NotAfter.After(now) {
		log.Warn("certificate expired", "not_after", stored.NotAfter)
		return nil, "certificado vencido", nil
	}
	if stored.NotAfter.Sub(now) <= CertWarn {
		log.Warn("certificate expires soon", "not_after", stored.NotAfter)
	}

	cert, err := account.OpenCertificate(a, stored, s.Box)
	if err != nil {
		log.Error("opening certificate", "err", err)
		return nil, "falha ao abrir o certificado", nil
	}
	sefaz, err := s.Dial(cert, a.Env)
	if err != nil {
		log.Error("building sefaz client", "err", err)
		return nil, "falha ao preparar a conexão com a SEFAZ", nil
	}
	return sefaz, "", nil
}

// pages calls NFeDistribuicaoDFe until there is nothing more, the limit says
// wait, or MaxPages calls were made. Each answer is stored with its NSU
// before the next call.
func (s *Scanner) pages(ctx context.Context, sefaz SEFAZ, a account.Account, log *slog.Logger) error {
	ult := a.UltNSU

	for page := 1; ; page++ {
		r, err := sefaz.DistNSU(ctx, a.UF, a.CNPJ, ult)
		answered := s.Now()
		wait := answered.Add(Wait)

		if err != nil {
			log.Warn("distdfe", "err", err)
			return s.finish(ctx, a.ID, account.ScanResult{UltNSU: ult, Next: wait, Message: "falha de comunicação com a SEFAZ"}, answered)
		}

		result := account.ScanResult{UltNSU: max(ult, r.UltNSU), Next: wait, CStat: r.CStat, Message: r.Motivo}

		switch r.CStat {
		case dfe.CStatNoDocs:
			logAnswer(log, r, result)
			return s.finish(ctx, a.ID, result, answered)
		case dfe.CStatOveruse:
			log.Warn("sefaz says consumo indevido", "motivo", r.Motivo)
			return s.finish(ctx, a.ID, result, answered)
		case dfe.CStatDocsFound:
		default:
			log.Warn("distdfe rejected", "cstat", r.CStat, "motivo", r.Motivo)
			result.UltNSU = ult
			return s.finish(ctx, a.ID, result, answered)
		}

		progressed := r.UltNSU > ult && len(r.Docs) > 0
		more := progressed && r.UltNSU < r.MaxNSU
		switch {
		case !progressed:
			// A 138 that does not move the NSU would loop forever.
			log.Warn("sefaz did not advance the NSU", "ult_nsu", ult, "answer_ult", r.UltNSU, "max_nsu", r.MaxNSU)
			result.Message = "a SEFAZ não avançou o NSU"
		case more && page >= MaxPages:
			// More to fetch: the next tick continues right away.
			result.Next = answered
			result.Message = "continua na próxima rodada"
		}

		if err := s.store(ctx, a, r.Docs, result, answered, log); err != nil {
			return err
		}
		logAnswer(log, r, result)
		ult = result.UltNSU

		if !more || page >= MaxPages {
			return nil
		}
	}
}

// logAnswer records a 137 or a 138 once it is stored, so the log shows every
// scan, not only the ones that went wrong.
func logAnswer(log *slog.Logger, r dfe.DistResult, result account.ScanResult) {
	log.Info("sefaz answered", "cstat", r.CStat, "motivo", r.Motivo, "ult_nsu", result.UltNSU,
		"max_nsu", r.MaxNSU, "docs", len(r.Docs), "next_scan_at", result.Next)
}

// store saves one answer and the NSU it reached in one transaction under the
// account lock.
func (s *Scanner) store(ctx context.Context, a account.Account, docs []dfe.Doc, r account.ScanResult, now time.Time, log *slog.Logger) error {
	return s.Accounts.WithLock(ctx, a.ID, func(tx account.LockedTx) error {
		for _, d := range docs {
			docID, isNew, err := s.Notes.SaveDoc(ctx, tx, a.ID, d, now)
			if err != nil {
				return err
			}
			if !isNew {
				continue
			}
			if err := s.apply(ctx, tx, docID, d, log); err != nil {
				return fmt.Errorf("NSU %d: %w", d.NSU, err)
			}
		}
		return s.Accounts.FinishScan(ctx, tx, r, now)
	})
}

// finish records how the run ended, without documents.
func (s *Scanner) finish(ctx context.Context, id uuid.UUID, r account.ScanResult, now time.Time) error {
	err := s.Accounts.WithLock(ctx, id, func(tx account.LockedTx) error {
		return s.Accounts.FinishScan(ctx, tx, r, now)
	})
	if err != nil {
		return fmt.Errorf("recording the scan of %s: %w", id, err)
	}
	return nil
}

// apply turns a new document into a note or an event. A document folio
// cannot read stays stored and is only logged, so one odd document never
// stops the scan.
func (s *Scanner) apply(ctx context.Context, tx account.LockedTx, docID uuid.UUID, d dfe.Doc, log *slog.Logger) error {
	a := tx.Account
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
		if issuedBy(e.Key, a.CNPJ) {
			// An event on a note the account issued, like the customer's
			// Confirmação da Operação: SEFAZ sends it to the issuer, but it
			// is not a note received. The document stays stored.
			log.Info("event on an issued note kept out of the notes", "nsu", d.NSU, "type", e.Type)
			return nil
		}
		return s.Notes.ApplyEvent(ctx, tx, a.ID, note.NewEvent{Event: e, Origin: note.OriginSEFAZ, DocID: &docID})
	}
}

// issuedBy tells whether the access key is of a note issued by cnpj: the
// issuer's CNPJ takes digits 7 to 20 of the key.
func issuedBy(key, cnpj string) bool {
	return len(key) == 44 && key[6:20] == cnpj
}

// ciencia sends the due Ciência of the account, one note at a time, each
// answer stored in its own transaction. A refusal or a network error only
// schedules a retry; it never stops the scan.
func (s *Scanner) ciencia(ctx context.Context, sefaz SEFAZ, a account.Account, log *slog.Logger) error {
	due, err := s.Notes.CienciaDue(ctx, a.ID, s.Now(), CienciaPerRun)
	if err != nil {
		return err
	}

	for _, n := range due {
		at := s.Now()
		r, sendErr := sefaz.Ciencia(ctx, a.CNPJ, n.Key, at)
		if sendErr != nil {
			log.Warn("ciência", "key", n.Key, "err", sendErr)
		}

		err := s.Accounts.WithLock(ctx, a.ID, func(tx account.LockedTx) error {
			if sendErr != nil {
				return s.Notes.CienciaFailed(ctx, tx, n.ID, at.Add(CienciaRetry))
			}

			event := note.NewEvent{
				Event:  dfe.Event{Key: n.Key, Type: dfe.EventCiencia, Seq: 1, Description: "Ciencia da Operacao", At: at},
				Origin: note.OriginFolio,
				CStat:  r.CStat,
				XML:    r.XML,
			}
			if err := s.Notes.ApplyEvent(ctx, tx, a.ID, event); err != nil {
				return err
			}
			return s.Notes.CienciaSent(ctx, tx, n.ID)
		})
		if err != nil {
			return errors.Join(fmt.Errorf("recording ciência of %s", n.Key), err)
		}
	}
	return nil
}
