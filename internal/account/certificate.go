package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/platform/crypt"
)

// ErrExpired is returned when the uploaded certificate is no longer valid.
var ErrExpired = errors.New("o certificado está vencido")

// CNPJMismatchError is returned when the certificate belongs to another
// CNPJ than the account.
type CNPJMismatchError struct {
	Cert    string
	Account string
}

// Error implements the error interface.
func (e *CNPJMismatchError) Error() string {
	return fmt.Sprintf("o certificado é do CNPJ %s, e a conta é do CNPJ %s", e.Cert, e.Account)
}

// UploadCertificate opens pfx with password, checks that it belongs to the
// account and is still valid, seals it with box and stores it, replacing the
// previous certificate. Errors never carry the password.
func UploadCertificate(ctx context.Context, st *Store, box *crypt.Box, a Account, pfx []byte, password string, now time.Time) (StoredCert, error) {
	cert, err := dfe.LoadPFX(pfx, password)
	if err != nil {
		return StoredCert{}, err
	}
	if cert.CNPJ != a.CNPJ {
		return StoredCert{}, &CNPJMismatchError{Cert: cert.CNPJ, Account: a.CNPJ}
	}
	if !cert.NotAfter.After(now) {
		return StoredCert{}, ErrExpired
	}

	sealedPFX, err := box.Seal(pfx)
	if err != nil {
		return StoredCert{}, fmt.Errorf("selando o certificado: %w", err)
	}
	sealedPass, err := box.Seal([]byte(password))
	if err != nil {
		return StoredCert{}, fmt.Errorf("selando a senha: %w", err)
	}

	sc := StoredCert{
		Holder:     cert.Holder,
		CNPJ:       cert.CNPJ,
		NotAfter:   cert.NotAfter,
		UploadedAt: now,
		KeyID:      box.KeyID(),
		PFX:        sealedPFX,
		Password:   sealedPass,
	}
	if err := st.PutCertificate(ctx, a.ID, sc); err != nil {
		return StoredCert{}, err
	}
	return sc, nil
}

// OpenCertificate unseals a stored certificate and opens it.
func OpenCertificate(sc StoredCert, box *crypt.Box) (*dfe.Certificate, error) {
	if sc.KeyID != box.KeyID() {
		return nil, fmt.Errorf("o certificado foi selado com a chave %q, e a chave atual é %q", sc.KeyID, box.KeyID())
	}

	pfx, err := box.Open(sc.PFX)
	if err != nil {
		return nil, fmt.Errorf("abrindo o certificado selado: %w", err)
	}
	pass, err := box.Open(sc.Password)
	if err != nil {
		return nil, fmt.Errorf("abrindo a senha selada: %w", err)
	}

	cert, err := dfe.LoadPFX(pfx, string(pass))
	if err != nil {
		return nil, fmt.Errorf("lendo o certificado guardado: %w", err)
	}
	return cert, nil
}
