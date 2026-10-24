// Package apiclient manages the programs that consume the folio API (Cadim,
// beep) and their API keys.
package apiclient

import (
	"errors"
	"time"
	"uuid"
)

// Errors returned by the store.
var (
	ErrNotFound = errors.New("cliente da API não encontrado")
	ErrInvalid  = errors.New("cliente da API inválido")
)

// KeyPrefix starts every API key, so a leaked one is easy to recognize.
const KeyPrefix = "fk_"

// Client is a program that calls the API.
type Client struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}
