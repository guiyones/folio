package dfetest_test

import (
	"bytes"
	"crypto/tls"
	"encoding/asn1"
	"net/http"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe/dfetest"
	"software.sslmate.com/src/go-pkcs12"
)

func TestNewCert(t *testing.T) {
	notAfter := time.Date(2027, 10, 1, 0, 0, 0, 0, time.UTC)
	pfx, pass := dfetest.NewCert(t, "11222333000181", notAfter)

	_, leaf, _, err := pkcs12.DecodeChain(pfx, pass)
	if err != nil {
		t.Fatalf("decoding pfx: %v", err)
	}
	if got, want := leaf.Subject.CommonName, "EMPRESA TESTE:11222333000181"; got != want {
		t.Errorf("CN = %q, want %q", got, want)
	}
	if !leaf.NotAfter.Equal(notAfter) {
		t.Errorf("NotAfter = %v, want %v", leaf.NotAfter, notAfter)
	}

	oid, err := asn1.Marshal(dfetest.OIDCNPJ)
	if err != nil {
		t.Fatalf("marshaling oid: %v", err)
	}
	var found bool
	for _, ext := range leaf.Extensions {
		if ext.Id.String() == "2.5.29.17" {
			found = bytes.Contains(ext.Value, oid) && bytes.Contains(ext.Value, []byte("11222333000181"))
		}
	}
	if !found {
		t.Error("subject alt name has no ICP-Brasil CNPJ")
	}
}

func TestNewCertWithoutCNPJ(t *testing.T) {
	pfx, pass := dfetest.NewCert(t, "", time.Now().AddDate(1, 0, 0))

	_, leaf, _, err := pkcs12.DecodeChain(pfx, pass)
	if err != nil {
		t.Fatalf("decoding pfx: %v", err)
	}
	if leaf.Subject.CommonName != dfetest.Holder {
		t.Errorf("CN = %q, want %q", leaf.Subject.CommonName, dfetest.Holder)
	}
	for _, ext := range leaf.Extensions {
		if ext.Id.String() == "2.5.29.17" {
			t.Error("certificate without CNPJ has a subject alt name")
		}
	}
}

func TestServerRequiresClientCert(t *testing.T) {
	srv := dfetest.NewServer(t, func(string, []byte) (int, []byte) {
		return http.StatusOK, []byte("<ok/>")
	})

	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: srv.Roots}}}
	if resp, err := noCert.Post(srv.URL, "application/soap+xml", nil); err == nil {
		resp.Body.Close() // the test fails on the next line anyway; a close error adds nothing
		t.Fatal("server accepted a client without certificate")
	}

	pfx, pass := dfetest.NewCert(t, "11222333000181", time.Now().AddDate(1, 0, 0))
	key, leaf, _, err := pkcs12.DecodeChain(pfx, pass)
	if err != nil {
		t.Fatalf("decoding pfx: %v", err)
	}
	withCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      srv.Roots,
		Certificates: []tls.Certificate{{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}},
	}}}
	resp, err := withCert.Post(srv.URL, `application/soap+xml; charset=utf-8; action="urn:teste"`, bytes.NewReader([]byte("<env/>")))
	if err != nil {
		t.Fatalf("posting with certificate: %v", err)
	}
	resp.Body.Close() // the body is not read; the test checks what the server recorded

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	if reqs[0].Action != "urn:teste" || string(reqs[0].Body) != "<env/>" || len(reqs[0].ClientCerts) == 0 {
		t.Errorf("request = %+v", reqs[0])
	}
}
