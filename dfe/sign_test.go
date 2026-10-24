package dfe_test

import (
	"bytes"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/dfe/dfetest"
)

const eventoID = "ID210210" + key + "01"

const evento = `<evento xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.00"><infEvento Id="` + eventoID + `">` +
	`<cOrgao>91</cOrgao><tpAmb>2</tpAmb><CNPJ>11222333000181</CNPJ><chNFe>` + key + `</chNFe>` +
	`<dhEvento>2026-10-24T10:00:00-03:00</dhEvento><tpEvento>210210</tpEvento><nSeqEvento>1</nSeqEvento>` +
	`<verEvento>1.00</verEvento><detEvento versao="1.00"><descEvento>Ciencia da Operacao</descEvento></detEvento>` +
	`</infEvento></evento>`

func loadCert(t *testing.T) *dfe.Certificate {
	t.Helper()
	pfx, pass := dfetest.NewCert(t, cnpj, time.Now().AddDate(1, 0, 0))
	c, err := dfe.LoadPFX(pfx, pass)
	if err != nil {
		t.Fatalf("load pfx: %v", err)
	}
	return c
}

func TestSignRoundTrip(t *testing.T) {
	signed, err := dfe.SignEnveloped([]byte(evento), eventoID, loadCert(t))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := dfe.VerifyEnveloped(signed); err != nil {
		t.Fatalf("verify: %v", err)
	}

	s := string(signed)
	if !strings.Contains(s, `</infEvento><Signature xmlns="http://www.w3.org/2000/09/xmldsig#">`) {
		t.Error("Signature is not right after infEvento")
	}
	if !strings.HasSuffix(s, "</Signature></evento>") {
		t.Error("Signature is not the last child of evento")
	}
	if !strings.Contains(s, `<Reference URI="#`+eventoID+`">`) {
		t.Error("Reference does not point to infEvento")
	}
}

// TestSignVerifiedByXMLSec checks the signature with an independent
// implementation, the one libxml2 users rely on.
func TestSignVerifiedByXMLSec(t *testing.T) {
	xmlsec, err := exec.LookPath("xmlsec1")
	if err != nil {
		t.Skip("xmlsec1 não instalado")
	}

	cert := loadCert(t)
	signed, err := dfe.SignEnveloped([]byte(evento), eventoID, cert)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	dir := t.TempDir()
	doc := filepath.Join(dir, "evento.xml")
	certFile := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(doc, signed, 0o600); err != nil {
		t.Fatalf("writing doc: %v", err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Leaf.Raw}), 0o600); err != nil {
		t.Fatalf("writing cert: %v", err)
	}

	out, err := exec.Command(xmlsec, "--verify",
		"--id-attr:Id", "http://www.portalfiscal.inf.br/nfe:infEvento",
		"--trusted-pem", certFile, doc).CombinedOutput()
	if err != nil {
		t.Fatalf("xmlsec1 refused the signature: %v\n%s", err, out)
	}

	tampered := bytes.Replace(signed, []byte("<tpAmb>2</tpAmb>"), []byte("<tpAmb>1</tpAmb>"), 1)
	if err := os.WriteFile(doc, tampered, 0o600); err != nil {
		t.Fatalf("writing doc: %v", err)
	}
	err = exec.Command(xmlsec, "--verify",
		"--id-attr:Id", "http://www.portalfiscal.inf.br/nfe:infEvento",
		"--trusted-pem", certFile, doc).Run()
	if err == nil {
		t.Error("xmlsec1 accepted a changed infEvento")
	}
}

func TestSignTamperFails(t *testing.T) {
	signed, err := dfe.SignEnveloped([]byte(evento), eventoID, loadCert(t))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	tampered := bytes.Replace(signed, []byte("<tpAmb>2</tpAmb>"), []byte("<tpAmb>1</tpAmb>"), 1)
	if err := dfe.VerifyEnveloped(tampered); err == nil {
		t.Error("verify accepted a changed infEvento")
	}

	broken := bytes.Replace(signed, []byte("<SignatureValue>"), []byte("<SignatureValue>AAAA"), 1)
	if err := dfe.VerifyEnveloped(broken); err == nil {
		t.Error("verify accepted a changed SignatureValue")
	}
}

func TestSignDeterministic(t *testing.T) {
	cert := loadCert(t)
	a, err := dfe.SignEnveloped([]byte(evento), eventoID, cert)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	b, err := dfe.SignEnveloped([]byte(evento), eventoID, cert)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two signatures of the same input differ")
	}
}

func TestSignUnknownID(t *testing.T) {
	if _, err := dfe.SignEnveloped([]byte(evento), "ID-que-nao-existe", loadCert(t)); err == nil {
		t.Error("signed an element that does not exist")
	}
}
