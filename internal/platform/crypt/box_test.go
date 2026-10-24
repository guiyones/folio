package crypt_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/guiyones/folio/internal/platform/crypt"
)

func newBox(t *testing.T, keyID string, fill byte) *crypt.Box {
	t.Helper()
	box, err := crypt.NewBox(keyID, bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	return box
}

func TestBoxRoundTrip(t *testing.T) {
	box := newBox(t, "k1", 7)

	sealed, err := box.Seal([]byte("certificado"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("certificado")) {
		t.Fatal("sealed value contains the plain text")
	}

	plain, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(plain) != "certificado" {
		t.Errorf("open = %q, want %q", plain, "certificado")
	}
}

func TestBoxNonceDiffers(t *testing.T) {
	box := newBox(t, "k1", 7)

	a, err := box.Seal([]byte("x"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	b, err := box.Seal([]byte("x"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Error("two seals of the same text are equal")
	}
}

func TestBoxTampered(t *testing.T) {
	box := newBox(t, "k1", 7)

	sealed, err := box.Seal([]byte("certificado"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	sealed[len(sealed)-1] ^= 1

	if _, err := box.Open(sealed); err == nil {
		t.Error("open accepted a tampered value")
	}
}

func TestBoxWrongKey(t *testing.T) {
	sealed, err := newBox(t, "k1", 7).Seal([]byte("certificado"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, err := newBox(t, "k1", 8).Open(sealed); err == nil {
		t.Error("open accepted another key")
	}
	if _, err := newBox(t, "k2", 7).Open(sealed); err == nil {
		t.Error("open accepted another key id")
	}
}

func TestBoxShort(t *testing.T) {
	if _, err := newBox(t, "k1", 7).Open([]byte("curto")); err == nil {
		t.Error("open accepted a short value")
	}
}

func TestBoxKeySize(t *testing.T) {
	if _, err := crypt.NewBox("k1", make([]byte, 16)); !errors.Is(err, crypt.ErrKeySize) {
		t.Errorf("err = %v, want ErrKeySize", err)
	}
}

func TestFromEnvMissing(t *testing.T) {
	t.Setenv("FOLIO_CERT_KEY", "")

	_, err := crypt.FromEnv()
	if err == nil || !strings.Contains(err.Error(), "FOLIO_CERT_KEY") {
		t.Errorf("err = %v, want it to name FOLIO_CERT_KEY", err)
	}
}

func TestFromEnv(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	t.Setenv("FOLIO_CERT_KEY", key)
	t.Setenv("FOLIO_CERT_KEY_ID", "")

	box, err := crypt.FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}
	if box.KeyID() != "k1" {
		t.Errorf("key id = %q, want k1", box.KeyID())
	}
}

func TestFromEnvWrongSizeHidesKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("chave-curta"))
	t.Setenv("FOLIO_CERT_KEY", key)

	_, err := crypt.FromEnv()
	if !errors.Is(err, crypt.ErrKeySize) {
		t.Fatalf("err = %v, want ErrKeySize", err)
	}
	if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "chave-curta") {
		t.Error("error shows the key")
	}
}
