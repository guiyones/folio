package main

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe/dfetest"
	"github.com/guiyones/folio/internal/platform/db/dbtest"
)

var idRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func run(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := admin(context.Background(), args, &out); err != nil {
		t.Fatalf("admin %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func TestAdminFlow(t *testing.T) {
	_, url := dbtest.NewWithURL(t)
	t.Setenv("FOLIO_DB_URL", url)
	t.Setenv("FOLIO_CERT_KEY", "BQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQU=")

	client := idRE.FindString(run(t, "client-create", "--name", "cadim"))
	if client == "" {
		t.Fatal("client-create printed no ID")
	}

	keyOut := run(t, "key-create", "--client", client)
	if !strings.HasPrefix(keyOut, "fk_") {
		t.Errorf("key-create = %q", keyOut)
	}

	acc := idRE.FindString(run(t, "account-create", "--client", client, "--name", "Franquia", "--cnpj", "11222333000181", "--uf", "41", "--env", "2"))
	if acc == "" {
		t.Fatal("account-create printed no ID")
	}

	pfx, pass := dfetest.NewCert(t, "11222333000181", time.Now().AddDate(1, 0, 0))
	path := t.TempDir() + "/cert.pfx"
	if err := os.WriteFile(path, pfx, 0o600); err != nil {
		t.Fatalf("writing pfx: %v", err)
	}
	t.Setenv("FOLIO_PFX_PASSWORD", pass)
	if out := run(t, "cert-upload", "--account", acc, "--pfx", path); !strings.Contains(out, "guardado") {
		t.Errorf("cert-upload = %q", out)
	}

	run(t, "account-pause", "--account", acc)
	run(t, "account-resume", "--account", acc)
	run(t, "cert-delete", "--account", acc)
	run(t, "key-revoke", "--prefix", keyOut[:12])
}

func TestAdminErrors(t *testing.T) {
	_, url := dbtest.NewWithURL(t)
	t.Setenv("FOLIO_DB_URL", url)

	cases := [][]string{
		{},
		{"nao-existe"},
		{"key-create"},
		{"key-create", "--client", "nao-e-uuid"},
		{"account-create", "--client", "0192b3c4-0000-7000-8000-000000000000", "--name", "x", "--cnpj", "11222333000181"},
	}
	for _, args := range cases {
		var out bytes.Buffer
		if err := admin(context.Background(), args, &out); err == nil {
			t.Errorf("admin %v succeeded", args)
		}
	}
}

// -h prints the options and exits 0, like any help.
func TestHelpIsNotAnError(t *testing.T) {
	for name, call := range map[string]func() error{
		"admin":         func() error { return admin(context.Background(), []string{"-h"}, &bytes.Buffer{}) },
		"admin migrate": func() error { return admin(context.Background(), []string{"migrate", "-h"}, &bytes.Buffer{}) },
		"consulta":      func() error { return consulta(context.Background(), []string{"-h"}, &bytes.Buffer{}) },
		"serve":         func() error { return serve(context.Background(), []string{"-h"}) },
	} {
		if err := call(); err != nil {
			t.Errorf("%s -h = %v, want nil", name, err)
		}
	}
}
