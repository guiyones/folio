package dfe_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/dfe/dfetest"
)

func TestLoadPFX(t *testing.T) {
	notAfter := time.Date(2027, 10, 1, 0, 0, 0, 0, time.UTC)
	pfx, pass := dfetest.NewCert(t, "11222333000181", notAfter)

	c, err := dfe.LoadPFX(pfx, pass)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.CNPJ != "11222333000181" {
		t.Errorf("CNPJ = %q", c.CNPJ)
	}
	if c.Holder != "EMPRESA TESTE" {
		t.Errorf("Holder = %q", c.Holder)
	}
	if !c.NotAfter.Equal(notAfter) {
		t.Errorf("NotAfter = %v, want %v", c.NotAfter, notAfter)
	}
	if len(c.TLS.Certificate) < 1 || c.TLS.PrivateKey == nil || c.Key == nil || c.Leaf == nil {
		t.Errorf("certificate is incomplete: %+v", c)
	}
}

func TestLoadPFXWrongPassword(t *testing.T) {
	pfx, _ := dfetest.NewCert(t, "11222333000181", time.Now().AddDate(1, 0, 0))

	_, err := dfe.LoadPFX(pfx, "senha-errada-123")
	if !errors.Is(err, dfe.ErrBadPassword) {
		t.Fatalf("err = %v, want ErrBadPassword", err)
	}
	if strings.Contains(err.Error(), "senha-errada-123") {
		t.Error("error shows the password")
	}
}

func TestLoadPFXGarbage(t *testing.T) {
	_, err := dfe.LoadPFX([]byte("não sou pfx"), "x")
	if !errors.Is(err, dfe.ErrInvalidPFX) || errors.Is(err, dfe.ErrBadPassword) {
		t.Errorf("err = %v, want ErrInvalidPFX", err)
	}
}

func TestLoadPFXNoCNPJ(t *testing.T) {
	pfx, pass := dfetest.NewCert(t, "", time.Now().AddDate(1, 0, 0))

	if _, err := dfe.LoadPFX(pfx, pass); !errors.Is(err, dfe.ErrNoCNPJ) {
		t.Errorf("err = %v, want ErrNoCNPJ", err)
	}
}
