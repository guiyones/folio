// Package note keeps what folio received from SEFAZ for each account: the
// raw documents by NSU, the notes they add up to and the events of each
// note. Every change to a note takes a new number from one sequence, the
// cursor clients read changes by.
//
// Writes run inside a transaction that already holds the account lock (see
// account.Store.LockDue and Lock), so the numbers of one account are
// committed in order and a client reading by cursor never skips a note.
package note

import (
	"errors"
	"time"
	"uuid"
)

// Errors returned by the store.
var (
	ErrNotFound = errors.New("nota não encontrada")
	ErrNoXML    = errors.New("a nota ainda não tem XML")
	ErrConflict = errors.New("a nota não está num estado que permita isso")
)

// Status is how far folio got with a note.
type Status string

// Note statuses.
const (
	StatusSummary   Status = "resumo"    // only the summary came
	StatusFull      Status = "completa"  // the full XML came
	StatusCancelled Status = "cancelada" // the issuer cancelled it
	StatusDenied    Status = "denegada"  // SEFAZ denied it: no fiscal value
)

// Ciencia is the state of the Ciência da Operação of a note.
type Ciencia string

// Ciência states.
const (
	CienciaPending       Ciencia = "pendente"
	CienciaDone          Ciencia = "feita"
	CienciaFailed        Ciencia = "falhou"
	CienciaNotApplicable Ciencia = "nao_aplica"
)

// Origins of an event.
const (
	OriginSEFAZ = "sefaz" // came through NFeDistribuicaoDFe
	OriginFolio = "folio" // folio sent it
)

// Note is a note of an account. Seq is its place in the change cursor.
type Note struct {
	ID              uuid.UUID
	AccountID       uuid.UUID
	Key             string
	EmitCNPJ        string
	EmitName        string
	TotalCents      int64
	IssuedAt        *time.Time
	Status          Status
	Ciencia         Ciencia
	CienciaAttempts int
	CienciaNextAt   *time.Time
	Seq             int64
	HasFull         bool
}

// Event is an event of a note.
type Event struct {
	Type        string
	Seq         int
	Description string
	At          time.Time
	Origin      string
	CStat       string
}
