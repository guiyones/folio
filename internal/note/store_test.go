package note_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/note"
	"github.com/guiyones/folio/internal/platform/db"
	"github.com/guiyones/folio/internal/platform/db/dbtest"
	"github.com/jmoiron/sqlx"
)

const key = "41261011222333000181550010000001231000001234"

var (
	now    = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	issued = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
)

type fixture struct {
	db      *sqlx.DB
	st      *note.Store
	account uuid.UUID
	client  uuid.UUID
	nsu     int64
}

func setup(t *testing.T) *fixture {
	t.Helper()
	conn := dbtest.New(t)
	f := &fixture{db: conn, st: note.NewStore(conn), account: uuid.NewV7(), client: uuid.NewV7()}

	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO clients (id, name, created_at) VALUES ($1, 'cadim', $2)`, []any{f.client.String(), now}},
		{`INSERT INTO accounts (id, name, cnpj, uf, env, next_scan_at, ciencia_cutoff, created_at)
		  VALUES ($1, 'Franquia', '11222333000181', 41, 2, $2, $2, $2)`, []any{f.account.String(), now}},
		{`INSERT INTO client_accounts (client_id, account_id) VALUES ($1, $2)`, []any{f.client.String(), f.account.String()}},
	}
	for _, s := range stmts {
		if _, err := conn.Exec(s.q, s.args...); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return f
}

// tx runs fn in a transaction and fails the test on error.
func (f *fixture) tx(t *testing.T, fn func(tx *sqlx.Tx) error) {
	t.Helper()
	if err := db.WithTx(context.Background(), f.db, fn); err != nil {
		t.Fatalf("tx: %v", err)
	}
}

func (f *fixture) doc(t *testing.T, schema string) uuid.UUID {
	t.Helper()
	f.nsu++
	var id uuid.UUID
	f.tx(t, func(tx *sqlx.Tx) error {
		var err error
		id, _, err = f.st.SaveDoc(context.Background(), tx, f.account, dfe.Doc{NSU: f.nsu, Schema: schema, XML: []byte("<" + schema + "/>")}, now)
		return err
	})
	return id
}

func (f *fixture) summary(t *testing.T, n dfe.Note, cutoff time.Time, auto bool) {
	t.Helper()
	doc := f.doc(t, "resNFe_v1.01.xsd")
	f.tx(t, func(tx *sqlx.Tx) error {
		return f.st.ApplySummary(context.Background(), tx, f.account, doc, n, cutoff, auto)
	})
}

func (f *fixture) full(t *testing.T, n dfe.Note) {
	t.Helper()
	doc := f.doc(t, "procNFe_v4.00.xsd")
	f.tx(t, func(tx *sqlx.Tx) error {
		return f.st.ApplyFull(context.Background(), tx, f.account, doc, n)
	})
}

func (f *fixture) event(t *testing.T, typ string) {
	t.Helper()
	f.tx(t, func(tx *sqlx.Tx) error {
		return f.st.ApplyEvent(context.Background(), tx, f.account, note.NewEvent{
			Event:  dfe.Event{Key: key, Type: typ, Seq: 1, Description: "evento", At: now},
			Origin: note.OriginSEFAZ,
		})
	})
}

func (f *fixture) only(t *testing.T) note.Note {
	t.Helper()
	notes, err := f.st.NotesAfter(context.Background(), f.account, 0, 10)
	if err != nil {
		t.Fatalf("notes after: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notes, want 1", len(notes))
	}
	return notes[0]
}

func sample() dfe.Note {
	return dfe.Note{Key: key, EmitCNPJ: "99888777000166", EmitName: "DISTRIBUIDORA", TotalCents: 123450, IssuedAt: issued, Situation: dfe.Authorized}
}

func TestSummaryThenFull(t *testing.T) {
	f := setup(t)

	f.summary(t, sample(), now.Add(-24*time.Hour), true)
	first := f.only(t)
	if first.Status != note.StatusSummary || first.Ciencia != note.CienciaPending || first.HasFull || first.TotalCents != 123450 {
		t.Errorf("after summary = %+v", first)
	}

	full := sample()
	full.TotalCents = 123451
	f.full(t, full)
	second := f.only(t)
	if second.Status != note.StatusFull || !second.HasFull || second.TotalCents != 123451 || second.Seq <= first.Seq {
		t.Errorf("after full = %+v (first seq %d)", second, first.Seq)
	}
	if second.Ciencia != note.CienciaNotApplicable {
		t.Errorf("pending ciência after the full XML = %q", second.Ciencia)
	}

	xml, kind, err := f.st.XML(context.Background(), second.ID)
	if err != nil || kind != dfe.KindFull || string(xml) != "<procNFe_v4.00.xsd/>" {
		t.Errorf("xml = %s, %q, %v", xml, kind, err)
	}
}

func TestFullThenSummary(t *testing.T) {
	f := setup(t)

	f.full(t, sample())
	other := sample()
	other.EmitName = "OUTRO NOME"
	f.summary(t, other, now.Add(-24*time.Hour), true)

	n := f.only(t)
	if n.Status != note.StatusFull || n.Ciencia != note.CienciaNotApplicable || n.EmitName != "DISTRIBUIDORA" {
		t.Errorf("note = %+v", n)
	}
}

func TestCancelSticks(t *testing.T) {
	f := setup(t)

	f.summary(t, sample(), now.Add(-24*time.Hour), true)
	f.event(t, dfe.EventCancel)
	if n := f.only(t); n.Status != note.StatusCancelled || n.Ciencia != note.CienciaNotApplicable {
		t.Errorf("after cancel = %+v", n)
	}

	f.full(t, sample())
	if n := f.only(t); n.Status != note.StatusCancelled {
		t.Errorf("full undid the cancellation: %+v", n)
	}
}

func TestEventBeforeSummary(t *testing.T) {
	f := setup(t)

	f.event(t, dfe.EventCancel)
	f.summary(t, sample(), now.Add(-24*time.Hour), true)

	n := f.only(t)
	if n.Status != note.StatusCancelled || n.Ciencia != note.CienciaNotApplicable || n.EmitName != "DISTRIBUIDORA" {
		t.Errorf("note = %+v", n)
	}
}

func TestEventDuplicate(t *testing.T) {
	f := setup(t)

	f.summary(t, sample(), now, true)
	f.event(t, dfe.EventCancel)
	f.event(t, dfe.EventCancel)

	events, err := f.st.Events(context.Background(), f.only(t).ID)
	if err != nil || len(events) != 1 || events[0].Type != dfe.EventCancel || events[0].Origin != note.OriginSEFAZ {
		t.Errorf("events = %+v, %v", events, err)
	}
}

func TestSaveDocIdempotent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	d := dfe.Doc{NSU: 7, Schema: "resNFe_v1.01.xsd", XML: []byte("<x/>")}

	var a, b uuid.UUID
	var newA, newB bool
	f.tx(t, func(tx *sqlx.Tx) error {
		var err error
		if a, newA, err = f.st.SaveDoc(ctx, tx, f.account, d, now); err != nil {
			return err
		}
		b, newB, err = f.st.SaveDoc(ctx, tx, f.account, d, now)
		return err
	})
	if a != b || !newA || newB {
		t.Errorf("first %v new=%v, second %v new=%v", a, newA, b, newB)
	}

	var count int
	if err := f.db.Get(&count, `SELECT COUNT(*) FROM dfe_docs`); err != nil || count != 1 {
		t.Errorf("docs = %d, %v", count, err)
	}
}

func TestCienciaCutoff(t *testing.T) {
	cases := []struct {
		name   string
		cutoff time.Time
		auto   bool
		want   note.Ciencia
	}{
		{"after cutoff", issued.Add(-time.Hour), true, note.CienciaPending},
		{"at cutoff", issued, true, note.CienciaPending},
		{"before cutoff", issued.Add(time.Hour), true, note.CienciaNotApplicable},
		{"auto off", issued.Add(-time.Hour), false, note.CienciaNotApplicable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t)
			f.summary(t, sample(), c.cutoff, c.auto)
			if got := f.only(t).Ciencia; got != c.want {
				t.Errorf("ciência = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCancelledSummaryNeedsNoCiencia(t *testing.T) {
	f := setup(t)
	n := sample()
	n.Situation = dfe.Cancelled

	f.summary(t, n, issued.Add(-time.Hour), true)
	if got := f.only(t); got.Status != note.StatusCancelled || got.Ciencia != note.CienciaNotApplicable {
		t.Errorf("note = %+v", got)
	}
}

func TestNotesAfter(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	for i := range 3 {
		n := sample()
		n.Key = key[:43] + string(rune('5'+i))
		f.summary(t, n, now, false)
	}

	page, err := f.st.NotesAfter(ctx, f.account, 0, 2)
	if err != nil || len(page) != 2 {
		t.Fatalf("first page = %d, %v", len(page), err)
	}
	rest, err := f.st.NotesAfter(ctx, f.account, page[1].Seq, 2)
	if err != nil || len(rest) != 1 {
		t.Fatalf("second page = %d, %v", len(rest), err)
	}
	if !(page[0].Seq < page[1].Seq && page[1].Seq < rest[0].Seq) {
		t.Errorf("seqs out of order: %d %d %d", page[0].Seq, page[1].Seq, rest[0].Seq)
	}

	empty, err := f.st.NotesAfter(ctx, f.account, rest[0].Seq, 2)
	if err != nil || len(empty) != 0 {
		t.Errorf("after the last = %d, %v", len(empty), err)
	}

	other, err := f.st.NotesAfter(ctx, uuid.NewV7(), 0, 10)
	if err != nil || len(other) != 0 {
		t.Errorf("other account = %d, %v", len(other), err)
	}
}

func TestForClient(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.summary(t, sample(), now, false)
	n := f.only(t)

	if got, err := f.st.ForClient(ctx, f.client, n.ID); err != nil || got.ID != n.ID {
		t.Errorf("own note = %+v, %v", got, err)
	}
	if _, err := f.st.ForClient(ctx, uuid.NewV7(), n.ID); !errors.Is(err, note.ErrNotFound) {
		t.Errorf("other client: err = %v, want ErrNotFound", err)
	}
}

func TestXMLSummaryOnly(t *testing.T) {
	f := setup(t)
	f.summary(t, sample(), now, false)

	_, kind, err := f.st.XML(context.Background(), f.only(t).ID)
	if err != nil || kind != dfe.KindSummary {
		t.Errorf("kind = %q, %v", kind, err)
	}

	f.event(t, dfe.EventCancel)
	stub := setup(t)
	stub.event(t, dfe.EventCancel)
	if _, _, err := stub.st.XML(context.Background(), stub.only(t).ID); !errors.Is(err, note.ErrNoXML) {
		t.Errorf("note without docs: err = %v, want ErrNoXML", err)
	}
}

func TestDeniedSummary(t *testing.T) {
	f := setup(t)
	n := sample()
	n.Situation = dfe.Denied

	f.summary(t, n, issued.Add(-time.Hour), true)
	if got := f.only(t); got.Status != note.StatusDenied || got.Ciencia != note.CienciaNotApplicable {
		t.Errorf("note = %+v", got)
	}

	f.full(t, sample())
	if got := f.only(t); got.Status != note.StatusDenied {
		t.Errorf("a full XML undid the denial: %+v", got)
	}
}

func TestDeniedFull(t *testing.T) {
	f := setup(t)
	n := sample()
	n.Situation = dfe.Denied

	f.full(t, n)
	if got := f.only(t); got.Status != note.StatusDenied || !got.HasFull {
		t.Errorf("note = %+v", got)
	}
}

func TestSummaryCancelledLaterDropsCiencia(t *testing.T) {
	f := setup(t)
	f.summary(t, sample(), issued.Add(-time.Hour), true)
	if got := f.only(t); got.Ciencia != note.CienciaPending {
		t.Fatalf("first summary = %+v", got)
	}

	cancelled := sample()
	cancelled.Situation = dfe.Cancelled
	f.summary(t, cancelled, issued.Add(-time.Hour), true)
	if got := f.only(t); got.Status != note.StatusCancelled || got.Ciencia != note.CienciaNotApplicable {
		t.Errorf("after the cancelled summary = %+v", got)
	}
}
