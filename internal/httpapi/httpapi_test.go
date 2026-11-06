package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/dfe/dfetest"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/apiclient"
	"github.com/guiyones/folio/internal/httpapi"
	"github.com/guiyones/folio/internal/note"
	"github.com/guiyones/folio/internal/platform/crypt"
	"github.com/guiyones/folio/internal/platform/db/dbtest"
	"github.com/jmoiron/sqlx"
)

const (
	cnpj = "11222333000181"
	key  = "41261011222333000181550010000001231000001234"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

type api struct {
	t        *testing.T
	db       *sqlx.DB
	srv      *httptest.Server
	accounts *account.Store
	notes    *note.Store
	clients  *apiclient.Store
	box      *crypt.Box
	now      time.Time
}

type tenant struct {
	client  apiclient.Client
	key     string
	account account.Account
}

func setup(t *testing.T) *api {
	t.Helper()
	conn := dbtest.New(t)
	box, err := crypt.NewBox("k1", bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatalf("box: %v", err)
	}

	a := &api{t: t, db: conn, accounts: account.NewStore(conn), notes: note.NewStore(conn), clients: apiclient.NewStore(conn), box: box, now: t0}
	a.srv = httptest.NewServer(httpapi.New(httpapi.Deps{
		Accounts: a.accounts,
		Notes:    a.notes,
		Clients:  a.clients,
		Box:      box,
		Now:      func() time.Time { return a.now },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// tenant creates an API client with a key and one account of the given CNPJ.
func (a *api) tenant(name, accountCNPJ string) tenant {
	a.t.Helper()
	ctx := context.Background()

	c, err := a.clients.Create(ctx, name, t0)
	if err != nil {
		a.t.Fatalf("client: %v", err)
	}
	k, err := a.clients.CreateKey(ctx, c.ID, t0)
	if err != nil {
		a.t.Fatalf("key: %v", err)
	}
	acc, err := a.accounts.Create(ctx, account.NewAccount{Name: name, CNPJ: accountCNPJ, UF: 41, Env: dfe.Homologation, ClientID: c.ID}, t0)
	if err != nil {
		a.t.Fatalf("account: %v", err)
	}
	return tenant{client: c, key: k, account: acc}
}

func (a *api) upload(acc account.Account, notAfter time.Time) {
	a.t.Helper()
	pfx, pass := dfetest.NewCert(a.t, acc.CNPJ, notAfter)
	if _, err := account.UploadCertificate(context.Background(), a.accounts, a.box, acc, pfx, pass, t0); err != nil {
		a.t.Fatalf("upload: %v", err)
	}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decoding %s: %v", r.body, err)
	}
}

func (r response) errorMessage(t *testing.T) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	r.json(t, &e)
	if e.Error == "" {
		t.Fatalf("body has no error: %s", r.body)
	}
	return e.Error
}

func (a *api) do(method, path, apiKey string, body io.Reader, contentType string) response {
	a.t.Helper()
	req, err := http.NewRequest(method, a.srv.URL+path, body)
	if err != nil {
		a.t.Fatalf("request: %v", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatalf("reading body: %v", err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: data}
}

func (a *api) get(path, apiKey string) response { return a.do(http.MethodGet, path, apiKey, nil, "") }
func (a *api) post(path, apiKey string) response {
	return a.do(http.MethodPost, path, apiKey, nil, "")
}

func certForm(t *testing.T, pfx []byte, password string) (io.Reader, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	f, err := w.CreateFormFile("pfx", "cert.pfx")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := f.Write(pfx); err != nil {
		t.Fatalf("writing pfx: %v", err)
	}
	if err := w.WriteField("password", password); err != nil {
		t.Fatalf("password field: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing form: %v", err)
	}
	return &b, w.FormDataContentType()
}

// --- auth and routing --------------------------------------------------------

func TestAuthMissing(t *testing.T) {
	t.Parallel()
	a := setup(t)
	if r := a.get("/v1/accounts", ""); r.status != http.StatusUnauthorized {
		t.Errorf("status = %d", r.status)
	}
	if r := a.get("/v1/accounts", "fk_inventada"); r.status != http.StatusUnauthorized || r.errorMessage(t) != "chave de API inválida" {
		t.Errorf("status = %d %s", r.status, r.body)
	}
}

func TestAuthRevoked(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	if err := a.clients.RevokeKey(context.Background(), apiclient.Prefix(tn.key), t0); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if r := a.get("/v1/accounts", tn.key); r.status != http.StatusUnauthorized {
		t.Errorf("status = %d", r.status)
	}
}

func TestUnknownRoute404(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	r := a.get("/v1/nada", tn.key)
	if r.status != http.StatusNotFound {
		t.Errorf("status = %d %s", r.status, r.body)
	}
	r.errorMessage(t)
}

func TestBadUUID400(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	r := a.get("/v1/accounts/nao-e-uuid", tn.key)
	if r.status != http.StatusBadRequest {
		t.Errorf("status = %d %s", r.status, r.body)
	}
	r.errorMessage(t)
}

// --- accounts ----------------------------------------------------------------

func TestAccountsOnlyOwn(t *testing.T) {
	t.Parallel()
	a := setup(t)
	cadim := a.tenant("cadim", cnpj)
	beep := a.tenant("beep", "99888777000166")

	var list struct {
		Accounts []map[string]any `json:"accounts"`
	}
	a.get("/v1/accounts", beep.key).json(t, &list)
	if len(list.Accounts) != 1 || list.Accounts[0]["id"] != beep.account.ID.String() {
		t.Errorf("beep sees %v", list.Accounts)
	}

	if r := a.get("/v1/accounts/"+cadim.account.ID.String(), beep.key); r.status != http.StatusNotFound {
		t.Errorf("other client's account: status = %d", r.status)
	}
	if r := a.get("/v1/accounts/"+uuid.NewV7().String(), beep.key); r.status != http.StatusNotFound {
		t.Errorf("unknown account: status = %d", r.status)
	}
	if r := a.get("/v1/accounts/"+cadim.account.ID.String(), cadim.key); r.status != http.StatusOK {
		t.Errorf("own account: status = %d", r.status)
	}
}

func TestCertificateUploadOK(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	pfx, pass := dfetest.NewCert(t, cnpj, t0.AddDate(1, 0, 0))

	body, ct := certForm(t, pfx, pass)
	r := a.do(http.MethodPut, "/v1/accounts/"+tn.account.ID.String()+"/certificate", tn.key, body, ct)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d %s", r.status, r.body)
	}
	var c map[string]any
	r.json(t, &c)
	if c["cnpj"] != cnpj || c["holder"] != dfetest.Holder || c["expired"] != false || c["expiring_soon"] != false {
		t.Errorf("certificate = %v", c)
	}
}

func TestCertificateUploadRefused(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	path := "/v1/accounts/" + tn.account.ID.String() + "/certificate"

	other, otherPass := dfetest.NewCert(t, "99888777000166", t0.AddDate(1, 0, 0))
	expired, expiredPass := dfetest.NewCert(t, cnpj, t0.AddDate(0, 0, -1))
	good, _ := dfetest.NewCert(t, cnpj, t0.AddDate(1, 0, 0))

	cases := []struct {
		name, password string
		pfx            []byte
	}{
		{"other cnpj", otherPass, other},
		{"expired", expiredPass, expired},
		{"wrong password", "senha-errada-xyz", good},
		{"not a pfx", "x", []byte("não sou pfx")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, ct := certForm(t, c.pfx, c.password)
			r := a.do(http.MethodPut, path, tn.key, body, ct)
			if r.status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d %s", r.status, r.body)
			}
			if strings.Contains(string(r.body), c.password) && c.password != "x" {
				t.Errorf("body shows the password: %s", r.body)
			}
		})
	}

	var acc map[string]any
	a.get("/v1/accounts/"+tn.account.ID.String(), tn.key).json(t, &acc)
	if acc["certificate"] != nil {
		t.Errorf("a refused certificate was stored: %v", acc["certificate"])
	}
}

func TestCertificateTooBig(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)

	body, ct := certForm(t, bytes.Repeat([]byte{1}, 1<<20+10), "x")
	r := a.do(http.MethodPut, "/v1/accounts/"+tn.account.ID.String()+"/certificate", tn.key, body, ct)
	if r.status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d %s", r.status, r.body)
	}
}

// A password over the limit is refused as such, not cut and then refused
// as the wrong password.
func TestCertificatePasswordTooLong(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	pfx, _ := dfetest.NewCert(t, cnpj, t0.AddDate(1, 0, 0))

	body, ct := certForm(t, pfx, strings.Repeat("s", 1025))
	r := a.do(http.MethodPut, "/v1/accounts/"+tn.account.ID.String()+"/certificate", tn.key, body, ct)
	if r.status != http.StatusBadRequest {
		t.Fatalf("status = %d %s", r.status, r.body)
	}
	if msg := r.errorMessage(t); !strings.Contains(msg, "senha longa demais") {
		t.Errorf("error = %q, want senha longa demais", msg)
	}
}

func TestCertificateUploadMissingField(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := w.WriteField("password", "x"); err != nil {
		t.Fatalf("field: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	r := a.do(http.MethodPut, "/v1/accounts/"+tn.account.ID.String()+"/certificate", tn.key, &b, w.FormDataContentType())
	if r.status != http.StatusBadRequest {
		t.Errorf("status = %d %s", r.status, r.body)
	}
}

func TestAccountJSONNeverHasSecrets(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	a.upload(tn.account, t0.AddDate(1, 0, 0))

	r := a.get("/v1/accounts/"+tn.account.ID.String(), tn.key)
	body := string(r.body)
	for _, bad := range []string{"pfx", "password", dfetest.Password, "key_id"} {
		if strings.Contains(body, bad) {
			t.Errorf("account JSON contains %q: %s", bad, body)
		}
	}
}

func TestExpiringSoon(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	a.upload(tn.account, t0.AddDate(0, 0, 10))

	var acc struct {
		Certificate struct {
			ExpiringSoon bool `json:"expiring_soon"`
			Expired      bool `json:"expired"`
		} `json:"certificate"`
		Env string `json:"env"`
	}
	a.get("/v1/accounts/"+tn.account.ID.String(), tn.key).json(t, &acc)
	if !acc.Certificate.ExpiringSoon || acc.Certificate.Expired || acc.Env != "homologacao" {
		t.Errorf("account = %+v", acc)
	}
}

func TestPauseResume(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	base := "/v1/accounts/" + tn.account.ID.String()

	var acc struct {
		Paused bool `json:"paused"`
	}
	a.post(base+"/pause", tn.key).json(t, &acc)
	if !acc.Paused {
		t.Error("pause did not pause")
	}
	a.post(base+"/resume", tn.key).json(t, &acc)
	if acc.Paused {
		t.Error("resume did not resume")
	}

	other := a.tenant("beep", "99888777000166")
	if r := a.post(base+"/pause", other.key); r.status != http.StatusNotFound {
		t.Errorf("pausing another client's account: status = %d", r.status)
	}
}

func TestScan(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	path := "/v1/accounts/" + tn.account.ID.String() + "/scan"

	if r := a.post(path, tn.key); r.status != http.StatusConflict {
		t.Errorf("without certificate: status = %d %s", r.status, r.body)
	}

	a.upload(tn.account, t0.AddDate(1, 0, 0))
	if r := a.post(path, tn.key); r.status != http.StatusAccepted {
		t.Errorf("due: status = %d %s", r.status, r.body)
	}

	if _, err := a.db.Exec(`UPDATE accounts SET next_scan_at = $1`, t0.Add(30*time.Minute)); err != nil {
		t.Fatalf("update: %v", err)
	}
	r := a.post(path, tn.key)
	if r.status != http.StatusTooManyRequests || r.header.Get("Retry-After") != "1800" {
		t.Errorf("too soon: status = %d, Retry-After %q, %s", r.status, r.header.Get("Retry-After"), r.body)
	}
	var tooSoon struct {
		NextScanAt time.Time `json:"next_scan_at"`
	}
	r.json(t, &tooSoon)
	if !tooSoon.NextScanAt.Equal(t0.Add(30 * time.Minute)) {
		t.Errorf("next_scan_at = %v", tooSoon.NextScanAt)
	}

	a.post("/v1/accounts/"+tn.account.ID.String()+"/pause", tn.key)
	if r := a.post(path, tn.key); r.status != http.StatusConflict {
		t.Errorf("paused: status = %d", r.status)
	}
}

// --- notes -------------------------------------------------------------------

// addNote stores a summary, and optionally the full note, for the account.
func (a *api) addNote(acc account.Account, k string, nsu int64, full bool) note.Note {
	a.t.Helper()
	ctx := context.Background()
	n := dfe.Note{Key: k, EmitCNPJ: "99888777000166", EmitName: "DISTRIBUIDORA", TotalCents: 1000, IssuedAt: t0.Add(time.Hour), Situation: dfe.Authorized}

	err := a.accounts.WithLock(ctx, acc.ID, func(tx account.LockedTx) error {
		doc, _, err := a.notes.SaveDoc(ctx, tx, acc.ID, dfe.Doc{NSU: nsu, Schema: "resNFe_v1.01.xsd", XML: []byte("<resNFe/>")}, t0)
		if err != nil {
			return err
		}
		if err := a.notes.ApplySummary(ctx, tx, acc.ID, doc, n, acc.CienciaCutoff, acc.AutoCiencia); err != nil {
			return err
		}
		if !full {
			return nil
		}
		doc, _, err = a.notes.SaveDoc(ctx, tx, acc.ID, dfe.Doc{NSU: nsu + 1000, Schema: "procNFe_v4.00.xsd", XML: []byte("<nfeProc/>")}, t0)
		if err != nil {
			return err
		}
		return a.notes.ApplyFull(ctx, tx, acc.ID, doc, n)
	})
	if err != nil {
		a.t.Fatalf("add note: %v", err)
	}

	notes, err := a.notes.NotesAfter(ctx, acc.ID, 0, 500)
	if err != nil {
		a.t.Fatalf("notes: %v", err)
	}
	for _, x := range notes {
		if x.Key == k {
			return x
		}
	}
	a.t.Fatalf("note %s not found", k)
	return note.Note{}
}

type noteList struct {
	Notes []struct {
		ID      string `json:"id"`
		Key     string `json:"key"`
		Seq     int64  `json:"seq"`
		Ciencia string `json:"ciencia"`
		HasFull bool   `json:"has_full_xml"`
	} `json:"notes"`
	NextAfter int64 `json:"next_after"`
}

func TestNotesCursor(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	for i := range 3 {
		a.addNote(tn.account, key[:43]+string(rune('5'+i)), int64(i+1), false)
	}
	base := "/v1/accounts/" + tn.account.ID.String() + "/notes"

	var first noteList
	a.get(base+"?limit=2", tn.key).json(t, &first)
	if len(first.Notes) != 2 || first.NextAfter != first.Notes[1].Seq {
		t.Fatalf("first page = %+v", first)
	}

	var second noteList
	a.get(base+"?limit=2&after="+itoa(first.NextAfter), tn.key).json(t, &second)
	if len(second.Notes) != 1 || second.Notes[0].Key != key[:43]+"7" {
		t.Fatalf("second page = %+v", second)
	}

	var empty noteList
	a.get(base+"?after="+itoa(second.NextAfter), tn.key).json(t, &empty)
	if len(empty.Notes) != 0 || empty.NextAfter != second.NextAfter {
		t.Errorf("after the end = %+v", empty)
	}
}

func TestNotesBadLimit(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	base := "/v1/accounts/" + tn.account.ID.String() + "/notes"

	for _, q := range []string{"?limit=0", "?limit=501", "?limit=abc", "?after=-1"} {
		r := a.get(base+q, tn.key)
		if r.status != http.StatusBadRequest {
			t.Errorf("%s: status = %d %s", q, r.status, r.body)
		}
	}
}

func TestNotesOtherClient(t *testing.T) {
	t.Parallel()
	a := setup(t)
	cadim := a.tenant("cadim", cnpj)
	beep := a.tenant("beep", "99888777000166")
	n := a.addNote(cadim.account, key, 1, false)

	for _, path := range []string{
		"/v1/accounts/" + cadim.account.ID.String() + "/notes",
		"/v1/notes/" + n.ID.String() + "/xml",
		"/v1/notes/" + n.ID.String() + "/events",
	} {
		if r := a.get(path, beep.key); r.status != http.StatusNotFound {
			t.Errorf("%s: status = %d", path, r.status)
		}
	}
	if r := a.post("/v1/notes/"+n.ID.String()+"/ciencia", beep.key); r.status != http.StatusNotFound {
		t.Errorf("ciência: status = %d", r.status)
	}
}

func TestNoteXMLFullOverSummary(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	summary := a.addNote(tn.account, key[:43]+"5", 1, false)
	full := a.addNote(tn.account, key[:43]+"6", 2, true)

	r := a.get("/v1/notes/"+summary.ID.String()+"/xml", tn.key)
	if r.status != http.StatusOK || r.header.Get("X-Folio-Kind") != "resNFe" || string(r.body) != "<resNFe/>" {
		t.Errorf("summary = %d %q %s", r.status, r.header.Get("X-Folio-Kind"), r.body)
	}
	r = a.get("/v1/notes/"+full.ID.String()+"/xml", tn.key)
	if r.status != http.StatusOK || r.header.Get("X-Folio-Kind") != "procNFe" || string(r.body) != "<nfeProc/>" ||
		!strings.HasPrefix(r.header.Get("Content-Type"), "application/xml") {
		t.Errorf("full = %d %q %s", r.status, r.header.Get("X-Folio-Kind"), r.body)
	}
}

func TestNoteEvents(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)
	n := a.addNote(tn.account, key, 1, false)

	ctx := context.Background()
	err := a.accounts.WithLock(ctx, tn.account.ID, func(tx account.LockedTx) error {
		return a.notes.ApplyEvent(ctx, tx, tn.account.ID, note.NewEvent{
			Event:  dfe.Event{Key: key, Type: dfe.EventCancel, Seq: 1, Description: "Cancelamento", At: t0},
			Origin: note.OriginSEFAZ,
		})
	})
	if err != nil {
		t.Fatalf("event: %v", err)
	}

	var list struct {
		Events []struct {
			Type   string `json:"type"`
			Origin string `json:"origin"`
		} `json:"events"`
	}
	a.get("/v1/notes/"+n.ID.String()+"/events", tn.key).json(t, &list)
	if len(list.Events) != 1 || list.Events[0].Type != dfe.EventCancel || list.Events[0].Origin != "sefaz" {
		t.Errorf("events = %+v", list.Events)
	}
}

func TestManualCiencia(t *testing.T) {
	t.Parallel()
	a := setup(t)
	tn := a.tenant("cadim", cnpj)

	// With automatic Ciência on, the note is already pending.
	auto := a.addNote(tn.account, key[:43]+"5", 1, false)
	if r := a.post("/v1/notes/"+auto.ID.String()+"/ciencia", tn.key); r.status != http.StatusConflict {
		t.Errorf("auto on: status = %d %s", r.status, r.body)
	}

	if _, err := a.db.Exec(`UPDATE accounts SET auto_ciencia = false`); err != nil {
		t.Fatalf("auto off: %v", err)
	}
	tn.account.AutoCiencia = false
	manual := a.addNote(tn.account, key[:43]+"6", 2, false)
	if manual.Ciencia != note.CienciaNotApplicable {
		t.Fatalf("manual note ciência = %q", manual.Ciencia)
	}

	r := a.post("/v1/notes/"+manual.ID.String()+"/ciencia", tn.key)
	if r.status != http.StatusAccepted {
		t.Fatalf("auto off: status = %d %s", r.status, r.body)
	}
	var n struct {
		Ciencia string `json:"ciencia"`
		Seq     int64  `json:"seq"`
	}
	r.json(t, &n)
	if n.Ciencia != "pendente" || n.Seq <= manual.Seq {
		t.Errorf("note = %+v (seq before %d)", n, manual.Seq)
	}

	if r := a.post("/v1/notes/"+manual.ID.String()+"/ciencia", tn.key); r.status != http.StatusConflict {
		t.Errorf("twice: status = %d", r.status)
	}

	full := a.addNote(tn.account, key[:43]+"7", 3, true)
	if r := a.post("/v1/notes/"+full.ID.String()+"/ciencia", tn.key); r.status != http.StatusConflict {
		t.Errorf("complete note: status = %d %s", r.status, r.body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
