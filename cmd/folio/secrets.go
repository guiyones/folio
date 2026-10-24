package main

import (
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"
)

// pfxPassword reads the certificate password from FOLIO_PFX_PASSWORD or,
// when it is empty, asks for it on the terminal without echo. It is never a
// flag, because flags end up in the shell history.
func pfxPassword() (string, error) {
	if p := os.Getenv("FOLIO_PFX_PASSWORD"); p != "" {
		return p, nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("defina FOLIO_PFX_PASSWORD ou rode num terminal para digitar a senha")
	}

	fmt.Fprint(os.Stderr, "senha do certificado: ")
	p, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("lendo a senha: %w", err)
	}
	return string(p), nil
}

// sefazRoots returns the system roots plus the PEM file in FOLIO_SEFAZ_CA,
// or nil (system roots only) when the variable is empty.
func sefazRoots() (*x509.CertPool, error) {
	path := os.Getenv("FOLIO_SEFAZ_CA")
	if path == "" {
		return nil, nil
	}

	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lendo FOLIO_SEFAZ_CA: %w", err)
	}

	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("lendo as raízes do sistema: %w", err)
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("FOLIO_SEFAZ_CA (%s) não tem certificado PEM", path)
	}
	return roots, nil
}
