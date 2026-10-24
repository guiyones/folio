// Package crypt seals secrets, like A1 certificates and their passwords,
// before they reach the database.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// ErrKeySize is returned when the key is not 32 bytes (AES-256).
var ErrKeySize = errors.New("a chave de cifra precisa ter 32 bytes")

// Box seals and opens data with AES-256-GCM. A sealed value is the 12-byte
// nonce followed by the ciphertext and its tag. The key ID is authenticated
// with the data, so a value sealed under one key never opens under another.
type Box struct {
	keyID string
	aead  cipher.AEAD
}

// NewBox returns a Box for a 32-byte key identified by keyID.
func NewBox(keyID string, key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, ErrKeySize
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating gcm: %w", err)
	}

	return &Box{keyID: keyID, aead: aead}, nil
}

// FromEnv builds a Box from FOLIO_CERT_KEY (32 bytes in base64) and
// FOLIO_CERT_KEY_ID (defaults to "k1"). Errors never include the key.
func FromEnv() (*Box, error) {
	raw := os.Getenv("FOLIO_CERT_KEY")
	if raw == "" {
		return nil, errors.New("FOLIO_CERT_KEY não definida")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("FOLIO_CERT_KEY não é base64 válido")
	}

	keyID := os.Getenv("FOLIO_CERT_KEY_ID")
	if keyID == "" {
		keyID = "k1"
	}

	box, err := NewBox(keyID, key)
	if err != nil {
		return nil, fmt.Errorf("FOLIO_CERT_KEY: %w", err)
	}
	return box, nil
}

// KeyID identifies the key that seals with this Box.
func (b *Box) KeyID() string {
	return b.keyID
}

// Seal encrypts plain with a fresh random nonce.
func (b *Box) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize(), b.aead.NonceSize()+len(plain)+b.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("reading nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plain, []byte(b.keyID)), nil
}

// Open decrypts a value sealed by Seal under the same key and key ID.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, errors.New("valor selado curto demais")
	}

	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(b.keyID))
	if err != nil {
		return nil, fmt.Errorf("opening sealed value: %w", err)
	}
	return plain, nil
}
