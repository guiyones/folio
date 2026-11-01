package scan_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/dfe/dfetest"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/note"
	"github.com/guiyones/folio/internal/platform/crypt"
	"github.com/guiyones/folio/internal/platform/db/dbtest"
	"github.com/guiyones/folio/internal/scan"
	"github.com/jmoiron/sqlx"
	"uuid"
)

const cnpj = "11222333000181"

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// fakeSEFAZ answers DistNSU from a script, one answer per call; the last
// answer repeats. Ciencia answers with cienciaErr, or success.
type fakeSEFAZ struct {
	mu         sync.Mutex
	script     []answer
	distCalls  []int64
	ciencia    []string
	cienciaErr error
	duplicate  bool
	block      chan struct{} // when set, DistNSU waits on it
	started    chan struct{} // when set, DistNSU signals it before waiting
}

type answer struct {
	r   dfe.DistResult
	err error
}

func (f *fakeSEFAZ) DistNSU(ctx context.Context, uf int, c string, ult int64) (dfe.DistResult, error) {
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.distCalls = append(f.distCalls, ult)
	i := min(len(f.distCalls)-1, len(f.script)-1)
	return f.script[i].r, f.script[i].err
}

func (f *fakeSEFAZ) Ciencia(ctx context.Context, c, key string, at time.Time) (dfe.EventResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ciencia = append(f.ciencia, key)
	if f.cienciaErr != nil {
		return dfe.EventResult{}, f.cienciaErr
	}
	if f.duplicate {
		return dfe.EventResult{CStat: "573", Duplicate: true, XML: []byte("<procEventoNFe/>")}, nil
	}
	return dfe.EventResult{CStat: "135", XML: []byte("<procEventoNFe/>")}, nil
}

type env struct {
	db       *sqlx.DB
	accounts *account.Store
	notes    *note.Store
	scanner  *scan.Scanner
	sefaz    *fakeSEFAZ
	account  account.Account
	now      time.Time
}

func setup(t *testing.T, script ...answer) *env {
	t.Helper()
	conn := dbtest.New(t)
	ctx := context.Background()

	box, err := crypt.NewBox("k1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("box: %v", err)
	}

	e := &env{db: conn, accounts: account.NewStore(conn), notes: note.NewStore(conn), sefaz: &fakeSEFAZ{script: script}, now: t0}

	client := uuid.NewV7()
	if _, err := conn.Exec(`INSERT INTO clients (id, name, created_at) VALUES ($1, 'cadim', $2)`, client.String(), t0); err != nil {
		t.Fatalf("client: %v", err)
	}
	e.account, err = e.accounts.Create(ctx, account.NewAccount{Name: "Franquia", CNPJ: cnpj, UF: 41, Env: dfe.Homologation, ClientID: client}, t0)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	pfx, pass := dfetest.NewCert(t, cnpj, t0.AddDate(1, 0, 0))
	if _, err := account.UploadCertificate(ctx, e.accounts, box, e.account, pfx, pass, t0); err != nil {
		t.Fatalf("certificate: %v", err)
	}

	e.scanner = &scan.Scanner{
		DB:       conn,
		Accounts: e.accounts,
		Notes:    e.notes,
		Box:      box,
		Dial:     func(*dfe.Certificate, dfe.Env) (scan.SEFAZ, error) { return e.sefaz, nil },
		Now:      func() time.Time { return e.now },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return e
}

func (e *env) run(t *testing.T) bool {
	t.Helper()
	ran, err := e.scanner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return ran
}

func (e *env) reload(t *testing.T) account.Account {
	t.Helper()
	a, err := e.accounts.ByID(context.Background(), e.account.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return a
}

func (e *env) notesAll(t *testing.T) []note.Note {
	t.Helper()
	notes, err := e.notes.NotesAfter(context.Background(), e.account.ID, 0, 100)
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	return notes
}

func (e *env) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := e.db.Get(&n, "SELECT COUNT(*) FROM "+table); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// key is the access key of a note issued by DISTRIBUIDORA (99888777000166)
// to the account.
func key(i int) string { return fmt.Sprintf("4126109988877700016655001000000%03d10000012%02d", i, i) }

// event is a procEventoNFe of type tp on the note k.
func event(nsu int, k, tp string) dfetest.ZipDoc {
	return dfetest.ZipDoc{NSU: int64(nsu), Schema: "procEventoNFe_v1.00.xsd", XML: `<procEventoNFe xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.00">` +
		`<evento versao="1.00"><infEvento><chNFe>` + k + `</chNFe><dhEvento>2026-10-24T10:00:00-03:00</dhEvento>` +
		`<tpEvento>` + tp + `</tpEvento><nSeqEvento>1</nSeqEvento><detEvento versao="1.00"><descEvento>evento</descEvento></detEvento>` +
		`</infEvento></evento></procEventoNFe>`}
}

// summary is a resNFe issued one hour after the account was created, so
// automatic Ciência applies.
func summary(i int) dfetest.ZipDoc {
	return dfetest.ZipDoc{NSU: int64(i), Schema: "resNFe_v1.01.xsd", XML: `<resNFe xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.01">` +
		`<chNFe>` + key(i) + `</chNFe><CNPJ>99888777000166</CNPJ><xNome>DISTRIBUIDORA</xNome>` +
		`<dhEmi>2026-10-24T10:00:00-03:00</dhEmi><vNF>10.00</vNF><cSitNFe>1</cSitNFe></resNFe>`}
}

func docs(cStat string, ult, max int64, zd ...dfetest.ZipDoc) answer {
	r := dfe.DistResult{CStat: cStat, Motivo: "motivo " + cStat, UltNSU: ult, MaxNSU: max}
	for _, d := range zd {
		r.Docs = append(r.Docs, dfe.Doc{NSU: d.NSU, Schema: d.Schema, XML: []byte(d.XML)})
	}
	return answer{r: r}
}

func TestScanPagesUntilMax(t *testing.T) {
	e := setup(t,
		docs("138", 2, 4, summary(1), summary(2)),
		docs("138", 4, 4, summary(3), summary(4)),
	)

	if !e.run(t) {
		t.Fatal("no account ran")
	}
	if got := e.sefaz.distCalls; len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("calls = %v, want [0 2]", got)
	}
	a := e.reload(t)
	if a.UltNSU != 4 || !a.NextScanAt.Equal(t0.Add(scan.Wait)) || a.LastCStat != "138" || a.LastScanAt == nil {
		t.Errorf("account = %+v", a)
	}
	if n := len(e.notesAll(t)); n != 4 {
		t.Errorf("notes = %d, want 4", n)
	}
}

func TestScan137Waits(t *testing.T) {
	e := setup(t, docs("137", 9, 9))

	e.run(t)
	a := e.reload(t)
	if len(e.sefaz.distCalls) != 1 || a.UltNSU != 9 || !a.NextScanAt.Equal(t0.Add(scan.Wait)) || a.LastCStat != "137" {
		t.Errorf("calls %v, account %+v", e.sefaz.distCalls, a)
	}

	if e.run(t) {
		t.Error("ran again before the hour")
	}
}

func TestScan656Waits(t *testing.T) {
	e := setup(t, docs("656", 0, 0))

	e.run(t)
	a := e.reload(t)
	if len(e.sefaz.distCalls) != 1 || !a.NextScanAt.Equal(t0.Add(scan.Wait)) || a.LastCStat != "656" {
		t.Errorf("calls %v, account %+v", e.sefaz.distCalls, a)
	}
}

func TestScanRejectedKeepsNSU(t *testing.T) {
	e := setup(t, docs("593", 50, 50))

	e.run(t)
	a := e.reload(t)
	if a.UltNSU != 0 || a.LastCStat != "593" || !a.NextScanAt.Equal(t0.Add(scan.Wait)) {
		t.Errorf("account = %+v", a)
	}
}

func TestScanErrorKeepsEarlierPages(t *testing.T) {
	e := setup(t,
		docs("138", 1, 5, summary(1)),
		answer{err: errors.New("conexão recusada")},
	)

	e.run(t)
	a := e.reload(t)
	if a.UltNSU != 1 || a.LastMessage != "conexão recusada" || !a.NextScanAt.Equal(t0.Add(scan.Wait)) {
		t.Errorf("account = %+v", a)
	}
	if n := e.count(t, "dfe_docs"); n != 1 {
		t.Errorf("docs = %d, want the first page kept", n)
	}
}

func TestScanErrorOnFirstPage(t *testing.T) {
	e := setup(t, answer{err: errors.New("timeout")})

	e.run(t)
	a := e.reload(t)
	if a.UltNSU != 0 || a.LastMessage != "timeout" || e.count(t, "dfe_docs") != 0 {
		t.Errorf("account = %+v", a)
	}
}

func TestScanNSUNeverGoesBack(t *testing.T) {
	e := setup(t, docs("137", 3, 3))
	if _, err := e.db.Exec(`UPDATE accounts SET ult_nsu = 10`); err != nil {
		t.Fatalf("set nsu: %v", err)
	}

	e.run(t)
	if a := e.reload(t); a.UltNSU != 10 {
		t.Errorf("ult_nsu = %d, want 10", a.UltNSU)
	}
}

func TestScanMaxPages(t *testing.T) {
	script := make([]answer, scan.MaxPages)
	for i := range script {
		script[i] = docs("138", int64(i+1), 1000, summary(i+1))
	}
	e := setup(t, script...)

	e.run(t)
	a := e.reload(t)
	if len(e.sefaz.distCalls) != scan.MaxPages || a.UltNSU != scan.MaxPages || !a.NextScanAt.Equal(t0) {
		t.Errorf("calls %d, account %+v", len(e.sefaz.distCalls), a)
	}
}

func TestScanExpiredCert(t *testing.T) {
	e := setup(t, docs("137", 0, 0))
	e.now = t0.AddDate(2, 0, 0)

	e.run(t)
	a := e.reload(t)
	if len(e.sefaz.distCalls) != 0 || a.LastMessage != "certificado vencido" || !a.NextScanAt.Equal(e.now.Add(scan.Wait)) {
		t.Errorf("calls %d, account %+v", len(e.sefaz.distCalls), a)
	}
}

func TestScanSkipsPausedAndNotDue(t *testing.T) {
	e := setup(t, docs("137", 0, 0))
	ctx := context.Background()

	if err := e.accounts.SetPaused(ctx, e.account.ID, true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if e.run(t) {
		t.Error("ran a paused account")
	}

	if err := e.accounts.SetPaused(ctx, e.account.ID, false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	e.now = t0.Add(-time.Minute)
	if e.run(t) {
		t.Error("ran an account that is not due")
	}
}

func TestScanWithoutCertificate(t *testing.T) {
	e := setup(t, docs("137", 0, 0))
	if err := e.accounts.DeleteCertificate(context.Background(), e.account.ID); err != nil {
		t.Fatalf("delete certificate: %v", err)
	}
	if e.run(t) {
		t.Error("ran an account without certificate")
	}
}

// TestScanReservesBeforeCallingSEFAZ shows that the next hour is committed
// before the call, so a second process (or the same one after a crash)
// cannot query the CNPJ again, and that no lock is held during the call.
func TestScanReservesBeforeCallingSEFAZ(t *testing.T) {
	e := setup(t, docs("137", 0, 0))
	e.sefaz.block = make(chan struct{})
	e.sefaz.started = make(chan struct{}, 1)

	first := make(chan error, 1)
	go func() {
		_, err := e.scanner.RunOnce(context.Background())
		first <- err
	}()
	<-e.sefaz.started

	// While SEFAZ is being called, the reservation is already committed and
	// the account row is free: pausing does not wait for the scan.
	if a := e.reload(t); !a.NextScanAt.Equal(t0.Add(scan.Wait)) {
		t.Errorf("next_scan_at during the call = %v, want the reservation", a.NextScanAt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := e.db.ExecContext(ctx, `UPDATE accounts SET last_message = 'x'`); err != nil {
		t.Errorf("the account row is locked during the SEFAZ call: %v", err)
	}

	ran, err := e.scanner.RunOnce(context.Background())
	if err != nil || ran {
		t.Errorf("second run = %v, %v; want it to find nothing", ran, err)
	}

	close(e.sefaz.block)
	if err := <-first; err != nil {
		t.Fatalf("first run: %v", err)
	}
	if n := len(e.sefaz.distCalls); n != 1 {
		t.Errorf("SEFAZ got %d calls, want 1", n)
	}
}

func TestScanSendsCiencia(t *testing.T) {
	e := setup(t, docs("138", 1, 1, summary(1)))

	e.run(t)
	if len(e.sefaz.ciencia) != 1 || e.sefaz.ciencia[0] != key(1) {
		t.Fatalf("ciência calls = %v", e.sefaz.ciencia)
	}
	n := e.notesAll(t)[0]
	if n.Ciencia != note.CienciaDone {
		t.Errorf("ciência = %q, want done", n.Ciencia)
	}
	events, err := e.notes.Events(context.Background(), n.ID)
	if err != nil || len(events) != 1 || events[0].Origin != note.OriginFolio || events[0].Type != dfe.EventCiencia || events[0].CStat != "135" {
		t.Errorf("events = %+v, %v", events, err)
	}
}

func TestScanCienciaDuplicateIsSuccess(t *testing.T) {
	e := setup(t, docs("138", 1, 1, summary(1)))
	e.sefaz.duplicate = true

	e.run(t)
	if n := e.notesAll(t)[0]; n.Ciencia != note.CienciaDone {
		t.Errorf("ciência = %q, want done", n.Ciencia)
	}
}

func TestScanCienciaRetries(t *testing.T) {
	e := setup(t, docs("138", 1, 1, summary(1)), docs("137", 1, 1))
	e.sefaz.cienciaErr = errors.New("SEFAZ fora do ar")

	e.run(t)
	if n := e.notesAll(t)[0]; n.Ciencia != note.CienciaPending || n.CienciaAttempts != 1 {
		t.Fatalf("after 1 failure = %+v", n)
	}
	if a := e.reload(t); a.UltNSU != 1 {
		t.Errorf("a failed ciência held back the scan: ult_nsu = %d", a.UltNSU)
	}

	for i := 2; i <= note.CienciaMaxAttempts; i++ {
		e.now = e.now.Add(scan.Wait)
		e.run(t)
		if got := len(e.sefaz.ciencia); got != i {
			t.Fatalf("after %d runs: %d ciência calls", i, got)
		}
	}
	if n := e.notesAll(t)[0]; n.Ciencia != note.CienciaFailed || n.CienciaAttempts != note.CienciaMaxAttempts {
		t.Errorf("after max failures = %+v", n)
	}

	e.now = e.now.Add(scan.Wait)
	e.run(t)
	if got := len(e.sefaz.ciencia); got != note.CienciaMaxAttempts {
		t.Errorf("a failed ciência was sent again: %d calls", got)
	}
}

func TestScanNoCienciaBeforeCutoff(t *testing.T) {
	old := summary(1)
	old.XML = `<resNFe xmlns="http://www.portalfiscal.inf.br/nfe"><chNFe>` + key(1) + `</chNFe><CNPJ>99888777000166</CNPJ>` +
		`<xNome>X</xNome><dhEmi>2026-09-01T10:00:00-03:00</dhEmi><vNF>1</vNF><cSitNFe>1</cSitNFe></resNFe>`
	e := setup(t, docs("138", 1, 1, old))

	e.run(t)
	if len(e.sefaz.ciencia) != 0 {
		t.Errorf("sent ciência for a note from before the account: %v", e.sefaz.ciencia)
	}
	if n := e.notesAll(t)[0]; n.Ciencia != note.CienciaNotApplicable {
		t.Errorf("ciência = %q", n.Ciencia)
	}
}

func TestScanKeepsUnknownDocuments(t *testing.T) {
	e := setup(t, docs("138", 2, 2,
		dfetest.ZipDoc{NSU: 1, Schema: "resCTe_v1.00.xsd", XML: "<resCTe/>"},
		dfetest.ZipDoc{NSU: 2, Schema: "resNFe_v1.01.xsd", XML: "<resNFe>quebrado</resNFe>"},
	))

	e.run(t)
	if n := e.count(t, "dfe_docs"); n != 2 {
		t.Errorf("docs = %d, want both kept", n)
	}
	if a := e.reload(t); a.UltNSU != 2 {
		t.Errorf("ult_nsu = %d", a.UltNSU)
	}
}

func TestScanLogsEachAnswer(t *testing.T) {
	e := setup(t, docs("138", 1, 1, summary(1)), docs("137", 1, 1))
	e.sefaz.cienciaErr = errors.New("x")
	var out bytes.Buffer
	e.scanner.Log = slog.New(slog.NewTextHandler(&out, nil))

	e.run(t)
	e.now = e.now.Add(scan.Wait)
	e.run(t)

	for _, want := range []string{
		"cstat=138 motivo=\"motivo 138\" ult_nsu=1 max_nsu=1 docs=1 next_scan_at=",
		"cstat=137 motivo=\"motivo 137\" ult_nsu=1 max_nsu=1 docs=0 next_scan_at=",
	} {
		if !strings.Contains(out.String(), "level=INFO msg=\"sefaz answered\"") || !strings.Contains(out.String(), want) {
			t.Errorf("log has no %q:\n%s", want, out.String())
		}
	}
}

func TestScanKeepsEventsOnOwnNotesOutOfNotes(t *testing.T) {
	// The account's customer confirmed a note the account issued: SEFAZ
	// sends the event to the issuer, but it is not a note received.
	own := "41261011222333000181550010000022411100003944"
	e := setup(t, docs("138", 2, 2, event(1, own, "210200"), event(2, key(2), "110111")))

	e.run(t)
	if n := e.count(t, "dfe_docs"); n != 2 {
		t.Errorf("docs = %d, want both kept", n)
	}
	notes := e.notesAll(t)
	if len(notes) != 1 || notes[0].Key != key(2) {
		t.Errorf("notes = %+v, want only the received one", notes)
	}
}

func TestScanRepeatedNSUAppliesOnce(t *testing.T) {
	e := setup(t, docs("138", 1, 1, summary(1)), docs("138", 1, 1, summary(1)))
	e.sefaz.cienciaErr = errors.New("x")

	e.run(t)
	e.now = e.now.Add(scan.Wait)
	e.run(t)

	if n := e.count(t, "dfe_docs"); n != 1 {
		t.Errorf("docs = %d", n)
	}
	if notes := e.notesAll(t); len(notes) != 1 {
		t.Errorf("notes = %d", len(notes))
	}
}

func TestScanStopsWhenNSUDoesNotMove(t *testing.T) {
	e := setup(t, docs("138", 0, 50))

	e.run(t)
	a := e.reload(t)
	if len(e.sefaz.distCalls) != 1 || a.UltNSU != 0 || !a.NextScanAt.Equal(t0.Add(scan.Wait)) {
		t.Errorf("calls %v, account %+v", e.sefaz.distCalls, a)
	}
}

// TestScanWaitCountsFromTheAnswer: a 137 that arrives late in a long run
// still waits a full hour from the answer, not from the start of the run.
func TestScanWaitCountsFromTheAnswer(t *testing.T) {
	e := setup(t, docs("138", 1, 2, summary(1)), docs("137", 2, 2))
	calls := 0
	e.scanner.Now = func() time.Time {
		calls++
		return t0.Add(time.Duration(calls) * 10 * time.Minute)
	}

	e.run(t)
	a := e.reload(t)
	if a.LastScanAt == nil || !a.NextScanAt.Equal(a.LastScanAt.Add(scan.Wait)) || !a.NextScanAt.After(t0.Add(scan.Wait)) {
		t.Errorf("next %v, last %v", a.NextScanAt, a.LastScanAt)
	}
}

func TestScanErrorStopsTheLoopUntilTheTick(t *testing.T) {
	e := setup(t, docs("137", 0, 0))
	if _, err := e.db.Exec(`DROP TABLE note_events, notes, dfe_docs`); err != nil {
		t.Fatalf("drop: %v", err)
	}

	// Ciência reads notes; with the table gone the run fails, and Loop must
	// stop for the tick instead of claiming again right away.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.scanner.Loop(ctx, time.Hour)
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done

	if n := len(e.sefaz.distCalls); n != 1 {
		t.Errorf("SEFAZ got %d calls, want 1", n)
	}
}
