package apiclient_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/guiyones/folio/internal/apiclient"
	"github.com/guiyones/folio/internal/platform/db/dbtest"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestCreateKeyAndLookup(t *testing.T) {
	db := dbtest.New(t)
	st := apiclient.NewStore(db)
	ctx := context.Background()

	c, err := st.Create(ctx, "cadim", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	key, err := st.CreateKey(ctx, c.ID, now)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if !strings.HasPrefix(key, "fk_") || len(key) < 40 {
		t.Errorf("key = %q", key)
	}

	got, err := st.ByKey(ctx, key)
	if err != nil || got.ID != c.ID || got.Name != "cadim" {
		t.Errorf("by key = %+v, %v", got, err)
	}

	for _, bad := range []string{key + "x", key[:len(key)-1], "", "fk_", "sem-prefixo"} {
		if _, err := st.ByKey(ctx, bad); !errors.Is(err, apiclient.ErrNotFound) {
			t.Errorf("ByKey(%q) err = %v, want ErrNotFound", bad, err)
		}
	}

	if err := st.RevokeKey(ctx, apiclient.Prefix(key), now); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := st.ByKey(ctx, key); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("revoked key: err = %v, want ErrNotFound", err)
	}
	if err := st.RevokeKey(ctx, apiclient.Prefix(key), now); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("second revoke: err = %v, want ErrNotFound", err)
	}
}

func TestKeyStoredHashed(t *testing.T) {
	db := dbtest.New(t)
	st := apiclient.NewStore(db)
	ctx := context.Background()

	c, err := st.Create(ctx, "cadim", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	key, err := st.CreateKey(ctx, c.ID, now)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	var hash []byte
	var prefix string
	if err := db.QueryRow(`SELECT hash, prefix FROM api_keys`).Scan(&hash, &prefix); err != nil {
		t.Fatalf("reading key row: %v", err)
	}
	if strings.Contains(string(hash), key) || len(hash) != 32 {
		t.Errorf("hash column holds %d bytes", len(hash))
	}
	if !strings.HasPrefix(key, prefix) || len(prefix) >= len(key) {
		t.Errorf("prefix = %q", prefix)
	}
}

func TestCreateKeyUnknownClient(t *testing.T) {
	st := apiclient.NewStore(dbtest.New(t))

	if _, err := st.CreateKey(context.Background(), uuid.NewV7(), now); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateInvalid(t *testing.T) {
	st := apiclient.NewStore(dbtest.New(t))

	if _, err := st.Create(context.Background(), "  ", now); !errors.Is(err, apiclient.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}
