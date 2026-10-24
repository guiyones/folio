// Package dfe talks to SEFAZ: A1 certificates, mTLS, SOAP, the documents
// returned by NFeDistribuicaoDFe and XML signatures. It has no database and
// knows nothing about folio, so other programs can import it.
package dfe

// Env is the SEFAZ environment (tpAmb).
type Env int

// SEFAZ environments.
const (
	Production   Env = 1
	Homologation Env = 2
)
