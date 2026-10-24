// Package account manages the CNPJs folio queries at SEFAZ: the account
// itself, its scan state and its sealed A1 certificate.
package account

import (
	"errors"
	"time"
	"uuid"

	"github.com/guiyones/folio/dfe"
)

// Errors returned by the account store.
var (
	ErrNotFound      = errors.New("conta não encontrada")
	ErrDuplicate     = errors.New("já existe uma conta para este CNPJ neste ambiente")
	ErrUnknownClient = errors.New("cliente da API não encontrado")
	ErrNoCertificate = errors.New("a conta não tem certificado")
	ErrInvalid       = errors.New("conta inválida")
)

// Account is a CNPJ that folio queries in one SEFAZ environment. UltNSU is
// the last NSU already stored; NextScanAt is the earliest time the scan may
// call SEFAZ again. Notes issued before CienciaCutoff never get an automatic
// Ciência da Operação.
type Account struct {
	ID            uuid.UUID
	Name          string
	CNPJ          string
	UF            int
	Env           dfe.Env
	UltNSU        int64
	NextScanAt    time.Time
	LastScanAt    *time.Time
	LastCStat     string
	LastMessage   string
	Paused        bool
	AutoCiencia   bool
	CienciaCutoff time.Time
	CreatedAt     time.Time
}

// NewAccount is what is needed to register an account for an API client.
// StartNSU is where the scan starts; 0 brings everything SEFAZ still keeps.
type NewAccount struct {
	Name     string
	CNPJ     string
	UF       int
	Env      dfe.Env
	StartNSU int64
	ClientID uuid.UUID
}

// StoredCert is the certificate of an account. PFX and Password are sealed
// with the crypt.Box identified by KeyID.
type StoredCert struct {
	Holder     string
	CNPJ       string
	NotAfter   time.Time
	UploadedAt time.Time
	KeyID      string
	PFX        []byte
	Password   []byte
}
