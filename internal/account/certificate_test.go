package account_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/dfe/dfetest"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/platform/crypt"
	"github.com/guiyones/folio/internal/platform/db/dbtest"
)

func newBox(t *testing.T) *crypt.Box {
	t.Helper()
	box, err := crypt.NewBox("k1", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	return box
}

func TestUploadOK(t *testing.T) {
	db := dbtest.New(t)
	st := account.NewStore(db)
	box := newBox(t)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pfx, pass := dfetest.NewCert(t, a.CNPJ, now.AddDate(1, 0, 0))

	sc, err := account.UploadCertificate(ctx, st, box, a, pfx, pass, now)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if sc.Holder != dfetest.Holder || sc.CNPJ != a.CNPJ || !sc.NotAfter.Equal(now.AddDate(1, 0, 0)) || sc.KeyID != "k1" {
		t.Errorf("stored = %+v", sc)
	}

	stored, err := st.Certificate(ctx, a.ID)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	plain, err := box.Open(stored.PFX, a.ID.String()+"/pfx")
	if err != nil || !bytes.Equal(plain, pfx) {
		t.Errorf("unsealed pfx differs (err %v)", err)
	}

	cert, err := account.OpenCertificate(a, stored, box)
	if err != nil || cert.CNPJ != a.CNPJ {
		t.Errorf("open = %v, %v", cert, err)
	}
}

func TestUploadStoredSealed(t *testing.T) {
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pfx, pass := dfetest.NewCert(t, a.CNPJ, now.AddDate(1, 0, 0))
	if _, err := account.UploadCertificate(ctx, st, newBox(t), a, pfx, pass, now); err != nil {
		t.Fatalf("upload: %v", err)
	}

	var rawPFX, rawPass []byte
	if err := db.QueryRow(`SELECT pfx, password FROM certificates`).Scan(&rawPFX, &rawPass); err != nil {
		t.Fatalf("reading row: %v", err)
	}
	if bytes.Equal(rawPFX, pfx) || bytes.Contains(rawPass, []byte(pass)) {
		t.Error("certificate or password stored in clear")
	}
}

func TestUploadMismatch(t *testing.T) {
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pfx, pass := dfetest.NewCert(t, "99888777000166", now.AddDate(1, 0, 0))

	_, err = account.UploadCertificate(ctx, st, newBox(t), a, pfx, pass, now)
	var mismatch *account.CNPJMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want CNPJMismatchError", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "99888777000166") || !strings.Contains(msg, a.CNPJ) || strings.Contains(msg, pass) {
		t.Errorf("message = %q", msg)
	}
	if _, err := st.Certificate(ctx, a.ID); !errors.Is(err, account.ErrNoCertificate) {
		t.Errorf("a refused certificate was stored: %v", err)
	}
}

func TestUploadExpired(t *testing.T) {
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pfx, pass := dfetest.NewCert(t, a.CNPJ, now.AddDate(0, 0, -1))

	if _, err := account.UploadCertificate(ctx, st, newBox(t), a, pfx, pass, now); !errors.Is(err, account.ErrExpired) {
		t.Errorf("err = %v, want ErrExpired", err)
	}
}

func TestUploadWrongPassword(t *testing.T) {
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pfx, _ := dfetest.NewCert(t, a.CNPJ, now.AddDate(1, 0, 0))

	if _, err := account.UploadCertificate(ctx, st, newBox(t), a, pfx, "errada", now); !errors.Is(err, dfe.ErrBadPassword) {
		t.Errorf("err = %v, want ErrBadPassword", err)
	}
}

func TestOpenCertificateOtherKey(t *testing.T) {
	db := dbtest.New(t)
	st := account.NewStore(db)
	ctx := context.Background()

	a, err := st.Create(ctx, newAccount(newClient(t, db, "cadim")), now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pfx, pass := dfetest.NewCert(t, a.CNPJ, now.AddDate(1, 0, 0))
	sc, err := account.UploadCertificate(ctx, st, newBox(t), a, pfx, pass, now)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	other, err := crypt.NewBox("k2", bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	if _, err := account.OpenCertificate(a, sc, other); err == nil {
		t.Error("opened a certificate sealed with another key")
	}

	// A sealed certificate copied to another account does not open there.
	b := a
	b.ID = a.ID
	b.ID[15] ^= 1
	if _, err := account.OpenCertificate(b, sc, newBox(t)); err == nil {
		t.Error("opened a certificate sealed for another account")
	}
}
