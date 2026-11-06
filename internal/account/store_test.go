package account_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/platform/db/dbtest"
	"github.com/jmoiron/sqlx"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func newClient(t *testing.T, db *sqlx.DB, name string) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	if _, err := db.Exec(`INSERT INTO clients (id, name, created_at) VALUES ($1, $2, $3)`, id.String(), name, now); err != nil {
		t.Fatalf("inserting client: %v", err)
	}
	return id
}

func newAccount(clientID uuid.UUID) account.NewAccount {
	return account.NewAccount{
		Name:     "Franquia",
		CNPJ:     "11222333000181",
		UF:       41,
		Env:      dfe.Homologation,
		StartNSU: 120,
		ClientID: clientID,
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()
	client := newClient(t, db, "cadim")

	a, err := st.Create(ctx, newAccount(client), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.ByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("by id: %v", err)
	}
	if got.CNPJ != "11222333000181" || got.UF != 41 || got.Env != dfe.Homologation || got.UltNSU != 120 {
		t.Errorf("account = %+v", got)
	}
	if !got.AutoCiencia || got.Paused || !got.CienciaCutoff.Equal(now) || !got.NextScanAt.Equal(now) || got.LastScanAt != nil {
		t.Errorf("defaults = %+v", got)
	}
}

func TestCreateInvalid(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	st := account.NewStore(db)
	client := newClient(t, db, "cadim")

	cases := map[string]func(*account.NewAccount){
		"name": func(na *account.NewAccount) { na.Name = "" },
		"cnpj": func(na *account.NewAccount) { na.CNPJ = "11.222.333/0001-81" },
		"uf":   func(na *account.NewAccount) { na.UF = 99 },
		"env":  func(na *account.NewAccount) { na.Env = 3 },
		"nsu":  func(na *account.NewAccount) { na.StartNSU = -1 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			na := newAccount(client)
			change(&na)
			if _, err := st.Create(context.Background(), na, now); !errors.Is(err, account.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCreateDuplicate(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()
	client := newClient(t, db, "cadim")

	if _, err := st.Create(ctx, newAccount(client), now); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.Create(ctx, newAccount(client), now); !errors.Is(err, account.ErrDuplicate) {
		t.Errorf("same env: err = %v, want ErrDuplicate", err)
	}

	prod := newAccount(client)
	prod.Env = dfe.Production
	if _, err := st.Create(ctx, prod, now); err != nil {
		t.Errorf("other env: %v", err)
	}
}

func TestCreateUnknownClient(t *testing.T) {
	t.Parallel()
	st := account.NewStore(dbtest.New(t))

	_, err := st.Create(context.Background(), newAccount(uuid.NewV7()), now)
	if !errors.Is(err, account.ErrUnknownClient) {
		t.Errorf("err = %v, want ErrUnknownClient", err)
	}
}

func TestForClient(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()
	cadim := newClient(t, db, "cadim")
	beep := newClient(t, db, "beep")

	a, err := st.Create(ctx, newAccount(cadim), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := st.ForClient(ctx, cadim, a.ID); err != nil {
		t.Errorf("own account: %v", err)
	}
	if _, err := st.ForClient(ctx, beep, a.ID); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("other client: err = %v, want ErrNotFound", err)
	}
	list, err := st.ListForClient(ctx, beep)
	if err != nil || len(list) != 0 {
		t.Errorf("other client list = %v, %v", list, err)
	}
}

func TestLinkClient(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()
	cadim := newClient(t, db, "cadim")
	beep := newClient(t, db, "beep")

	a, err := st.Create(ctx, newAccount(cadim), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for range 2 {
		if err := st.LinkClient(ctx, a.ID, beep); err != nil {
			t.Fatalf("link: %v", err)
		}
	}

	for _, c := range []uuid.UUID{cadim, beep} {
		list, err := st.ListForClient(ctx, c)
		if err != nil || len(list) != 1 || list[0].ID != a.ID {
			t.Errorf("client %v list = %v, %v", c, list, err)
		}
	}

	if err := st.LinkClient(ctx, uuid.NewV7(), beep); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("unknown account: err = %v, want ErrNotFound", err)
	}
	if err := st.LinkClient(ctx, a.ID, uuid.NewV7()); !errors.Is(err, account.ErrUnknownClient) {
		t.Errorf("unknown client: err = %v, want ErrUnknownClient", err)
	}
}

func TestSetPaused(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SetPaused(ctx, a.ID, true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	got, err := st.ByID(ctx, a.ID)
	if err != nil || !got.Paused {
		t.Errorf("paused = %v, %v", got.Paused, err)
	}

	if err := st.SetPaused(ctx, uuid.NewV7(), true); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("unknown account: err = %v, want ErrNotFound", err)
	}
}

func TestCertificate(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := st.Certificate(ctx, a.ID); !errors.Is(err, account.ErrNoCertificate) {
		t.Errorf("before upload: err = %v, want ErrNoCertificate", err)
	}

	first := account.StoredCert{Holder: "A", CNPJ: a.CNPJ, NotAfter: now.AddDate(1, 0, 0), UploadedAt: now, KeyID: "k1", PFX: []byte{1}, Password: []byte{2}}
	second := first
	second.Holder, second.PFX, second.UploadedAt = "B", []byte{3}, now.Add(time.Hour)

	for _, c := range []account.StoredCert{first, second} {
		if err := st.PutCertificate(ctx, a.ID, c); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	got, err := st.Certificate(ctx, a.ID)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	if got.Holder != "B" || !bytes.Equal(got.PFX, []byte{3}) || !got.UploadedAt.Equal(second.UploadedAt) {
		t.Errorf("certificate = %+v, want the second one", got)
	}

	if err := st.PutCertificate(ctx, uuid.NewV7(), first); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("unknown account: err = %v, want ErrNotFound", err)
	}

	if err := st.DeleteCertificate(ctx, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.DeleteCertificate(ctx, a.ID); !errors.Is(err, account.ErrNoCertificate) {
		t.Errorf("second delete: err = %v, want ErrNoCertificate", err)
	}
}

func TestWithLock(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// fn gets the locked account; a write in it commits.
	err = st.WithLock(ctx, a.ID, func(tx account.LockedTx) error {
		if tx.Account.ID != a.ID {
			t.Errorf("locked account = %s, want %s", tx.Account.ID, a.ID)
		}
		return st.FinishScan(ctx, tx, account.ScanResult{UltNSU: 130, Next: now, Message: "ok"}, now)
	})
	if err != nil {
		t.Fatalf("with lock: %v", err)
	}

	// An error from fn rolls the write back.
	boom := errors.New("boom")
	err = st.WithLock(ctx, a.ID, func(tx account.LockedTx) error {
		if err := st.FinishScan(ctx, tx, account.ScanResult{UltNSU: 999, Next: now}, now); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	got, err := st.ByID(ctx, a.ID)
	if err != nil || got.UltNSU != 130 || got.LastMessage != "ok" {
		t.Errorf("account = %+v, %v; want ult_nsu 130 kept", got, err)
	}

	if err := st.WithLock(ctx, uuid.NewV7(), func(account.LockedTx) error { return nil }); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("unknown account: err = %v, want ErrNotFound", err)
	}
}
