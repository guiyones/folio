// Package dfetest contains supporting code for testing code that talks to
// SEFAZ: throwaway A1 certificates and a fake SEFAZ that requires mTLS.
package dfetest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// Password protects every PFX built by NewCert.
const Password = "teste"

// Holder is the company name written in every certificate built by NewCert.
const Holder = "EMPRESA TESTE"

// OIDCNPJ is the ICP-Brasil otherName that carries the CNPJ of an e-CNPJ
// certificate inside the SubjectAltName.
var OIDCNPJ = asn1.ObjectIdentifier{2, 16, 76, 1, 3, 3}

var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

// NewCert builds a self-signed A1 certificate for cnpj, valid until notAfter,
// and returns it packed as a PFX with its password.
//
// Like a real e-CNPJ, the CNPJ goes both in the SubjectAltName (OIDCNPJ, as
// an OCTET STRING) and at the end of the CN ("EMPRESA TESTE:<cnpj>"). An
// empty cnpj builds a certificate with neither, to test the missing case.
func NewCert(t *testing.T, cnpj string, notAfter time.Time) (pfx []byte, password string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: Holder},
		NotBefore:    notAfter.AddDate(-1, 0, 0),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if cnpj != "" {
		tpl.Subject.CommonName = Holder + ":" + cnpj
		san, err := cnpjSubjectAltName(cnpj)
		if err != nil {
			t.Fatalf("building subject alt name: %v", err)
		}
		tpl.ExtraExtensions = []pkix.Extension{san}
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing certificate: %v", err)
	}

	pfx, err = pkcs12.Modern.Encode(key, leaf, nil, Password)
	if err != nil {
		t.Fatalf("encoding pfx: %v", err)
	}

	return pfx, Password
}

// cnpjSubjectAltName encodes a SubjectAltName with a single otherName:
//
//	GeneralName ::= [0] IMPLICIT SEQUENCE { type-id OID, value [0] EXPLICIT ANY }
func cnpjSubjectAltName(cnpj string) (pkix.Extension, error) {
	value, err := asn1.Marshal([]byte(cnpj))
	if err != nil {
		return pkix.Extension{}, err
	}

	otherName, err := asn1.Marshal(struct {
		ID    asn1.ObjectIdentifier
		Value asn1.RawValue
	}{
		ID:    OIDCNPJ,
		Value: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: value},
	})
	if err != nil {
		return pkix.Extension{}, err
	}

	// Marshal wrote a SEQUENCE; the GeneralName needs the same content under
	// the implicit [0] tag.
	var seq asn1.RawValue
	if _, err := asn1.Unmarshal(otherName, &seq); err != nil {
		return pkix.Extension{}, err
	}
	name := asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: seq.Bytes}

	san, err := asn1.Marshal([]asn1.RawValue{name})
	if err != nil {
		return pkix.Extension{}, err
	}

	return pkix.Extension{Id: oidSubjectAltName, Value: san}, nil
}
