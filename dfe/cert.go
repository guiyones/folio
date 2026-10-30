package dfe

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// Errors returned by LoadPFX. None of them carries the password.
var (
	ErrBadPassword = errors.New("senha do certificado incorreta")
	ErrNoCNPJ      = errors.New("o certificado não tem CNPJ (não é um e-CNPJ ICP-Brasil)")
	ErrNotRSA      = errors.New("a chave do certificado não é RSA")
	ErrInvalidPFX  = errors.New("o arquivo não é um certificado PFX válido")
)

// oidCNPJ is the ICP-Brasil otherName, inside the SubjectAltName, that
// carries the CNPJ of an e-CNPJ certificate.
var oidCNPJ = asn1.ObjectIdentifier{2, 16, 76, 1, 3, 3}

var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

var (
	cnpjRE       = regexp.MustCompile(`^[0-9]{14}$`)
	cnSuffixCNPJ = regexp.MustCompile(`:([0-9]{14})$`)
)

// Certificate is an A1 certificate opened from its PFX: what the TLS client
// presents to SEFAZ, the key that signs XML, and who it belongs to.
type Certificate struct {
	TLS      tls.Certificate
	Key      *rsa.PrivateKey
	Leaf     *x509.Certificate
	Chain    []*x509.Certificate
	CNPJ     string
	Holder   string
	NotAfter time.Time
}

// LoadPFX opens an A1 certificate (PKCS#12, the .pfx or .p12 file) with its
// password.
//
// The CNPJ comes from the ICP-Brasil otherName 2.16.76.1.3.3 and, when the
// certificate lacks it, from the ":<14 digits>" suffix of the CN. Holder is
// the CN without that suffix.
//
// A file in BER, as some issuers deliver it, is rewritten in DER first.
func LoadPFX(data []byte, password string) (*Certificate, error) {
	key, leaf, chain, err := pkcs12.DecodeChain(data, password)
	if err != nil && !errors.Is(err, pkcs12.ErrIncorrectPassword) {
		der, berErr := pfxToDER(data, password)
		switch {
		case errors.Is(berErr, ErrBadPassword):
			return nil, ErrBadPassword
		case berErr != nil:
			return nil, fmt.Errorf("%w: %w (lido como BER: %w)", ErrInvalidPFX, err, berErr)
		}
		key, leaf, chain, err = pkcs12.DecodeChain(der, password)
	}
	if err != nil {
		if errors.Is(err, pkcs12.ErrIncorrectPassword) {
			return nil, ErrBadPassword
		}
		return nil, fmt.Errorf("%w: %w", ErrInvalidPFX, err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, ErrNotRSA
	}

	cnpj, err := certCNPJ(leaf)
	if err != nil {
		return nil, err
	}

	raw := [][]byte{leaf.Raw}
	for _, c := range chain {
		raw = append(raw, c.Raw)
	}

	return &Certificate{
		TLS:      tls.Certificate{Certificate: raw, PrivateKey: rsaKey, Leaf: leaf},
		Key:      rsaKey,
		Leaf:     leaf,
		Chain:    chain,
		CNPJ:     cnpj,
		Holder:   strings.TrimSpace(cnSuffixCNPJ.ReplaceAllString(leaf.Subject.CommonName, "")),
		NotAfter: leaf.NotAfter,
	}, nil
}

// certCNPJ finds the CNPJ of an e-CNPJ certificate.
func certCNPJ(leaf *x509.Certificate) (string, error) {
	for _, ext := range leaf.Extensions {
		if !ext.Id.Equal(oidSubjectAltName) {
			continue
		}
		cnpj, err := sanCNPJ(ext.Value)
		if err != nil {
			return "", fmt.Errorf("lendo o SubjectAltName: %w", err)
		}
		if cnpj != "" {
			return cnpj, nil
		}
	}

	if m := cnSuffixCNPJ.FindStringSubmatch(leaf.Subject.CommonName); m != nil {
		return m[1], nil
	}

	return "", ErrNoCNPJ
}

// sanCNPJ reads the CNPJ otherName out of a DER SubjectAltName, or returns
// "" when there is none:
//
//	SubjectAltName ::= SEQUENCE OF GeneralName
//	GeneralName    ::= [0] IMPLICIT SEQUENCE { type-id OID, value [0] EXPLICIT ANY }
//
// Real certificates carry the value as an OCTET STRING or as a text string;
// both are accepted.
func sanCNPJ(der []byte) (string, error) {
	var names []asn1.RawValue
	if _, err := asn1.Unmarshal(der, &names); err != nil {
		return "", err
	}

	for _, name := range names {
		if name.Class != asn1.ClassContextSpecific || name.Tag != 0 {
			continue // not an otherName
		}

		var id asn1.ObjectIdentifier
		rest, err := asn1.Unmarshal(name.Bytes, &id)
		if err != nil {
			return "", err
		}
		if !id.Equal(oidCNPJ) {
			continue
		}

		var explicit asn1.RawValue
		if _, err := asn1.Unmarshal(rest, &explicit); err != nil {
			return "", err
		}
		var value asn1.RawValue
		if _, err := asn1.Unmarshal(explicit.Bytes, &value); err != nil {
			return "", err
		}

		cnpj := strings.TrimSpace(string(value.Bytes))
		if !cnpjRE.MatchString(cnpj) {
			return "", fmt.Errorf("CNPJ %q no certificado não tem 14 dígitos", cnpj)
		}
		return cnpj, nil
	}

	return "", nil
}
