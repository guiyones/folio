package dfe_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/dfe/dfetest"
)

func eventReturn(lote, cStat, motivo string) []byte {
	return dfetest.Envelope(`<nfeRecepcaoEventoNFResult xmlns="http://www.portalfiscal.inf.br/nfe/wsdl/NFeRecepcaoEvento4">` +
		`<retEnvEvento xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.00"><idLote>1</idLote><tpAmb>2</tpAmb>` +
		`<cOrgao>91</cOrgao><cStat>` + lote + `</cStat><xMotivo>Lote de evento processado</xMotivo>` +
		`<retEvento versao="1.00"><infEvento><tpAmb>2</tpAmb><cOrgao>91</cOrgao><cStat>` + cStat + `</cStat>` +
		`<xMotivo>` + motivo + `</xMotivo><chNFe>` + key + `</chNFe><tpEvento>210210</tpEvento><nSeqEvento>1</nSeqEvento>` +
		`<nProt>891260000000003</nProt></infEvento></retEvento></retEnvEvento></nfeRecepcaoEventoNFResult>`)
}

var at = time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)

func TestCiencia135(t *testing.T) {
	c, srv := newClient(t, answer(eventReturn("128", "135", "Evento registrado e vinculado a NF-e")))

	r, err := c.Ciencia(context.Background(), cnpj, key, at)
	if err != nil {
		t.Fatalf("ciencia: %v", err)
	}
	if r.CStat != "135" || r.Duplicate || r.Protocol != "891260000000003" {
		t.Errorf("result = %+v", r)
	}

	if !strings.HasPrefix(string(r.XML), "<procEventoNFe") {
		t.Errorf("XML = %.80s", r.XML)
	}
	if err := dfe.VerifyEnveloped(r.XML); err != nil {
		t.Errorf("procEventoNFe signature: %v", err)
	}
	if e, err := dfe.ParseEvent(dfe.KindEvent, r.XML); err != nil || e.Type != dfe.EventCiencia || e.Key != key {
		t.Errorf("parse procEventoNFe = %+v, %v", e, err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	body := string(reqs[0].Body)
	for _, want := range []string{
		`Id="ID210210` + key + `01"`,
		"<cOrgao>91</cOrgao>",
		"<dhEvento>2026-10-24T10:00:00-03:00</dhEvento>",
		"<descEvento>Ciencia da Operacao</descEvento>",
		"<idLote>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("request lacks %s", want)
		}
	}
	if !strings.HasSuffix(reqs[0].Action, "/nfeRecepcaoEvento") {
		t.Errorf("action = %q", reqs[0].Action)
	}

	start := strings.Index(body, "<evento ")
	end := strings.Index(body, "</evento>")
	if start < 0 || end < 0 {
		t.Fatal("request has no evento")
	}
	if err := dfe.VerifyEnveloped([]byte(body[start : end+len("</evento>")])); err != nil {
		t.Errorf("sent evento signature: %v", err)
	}
}

func TestCiencia573Duplicate(t *testing.T) {
	c, _ := newClient(t, answer(eventReturn("128", "573", "Duplicidade de evento")))

	r, err := c.Ciencia(context.Background(), cnpj, key, at)
	if err != nil {
		t.Fatalf("ciencia: %v", err)
	}
	if !r.Duplicate {
		t.Errorf("result = %+v, want Duplicate", r)
	}
}

func TestCiencia297Rejected(t *testing.T) {
	c, _ := newClient(t, answer(eventReturn("128", "297", "Assinatura difere do calculado")))

	_, err := c.Ciencia(context.Background(), cnpj, key, at)
	var rej *dfe.RejectedError
	if !errors.As(err, &rej) || rej.CStat != "297" {
		t.Errorf("err = %v, want RejectedError 297", err)
	}
}

func TestCienciaLoteRejected(t *testing.T) {
	c, _ := newClient(t, answer(eventReturn("215", "", "")))

	_, err := c.Ciencia(context.Background(), cnpj, key, at)
	var rej *dfe.RejectedError
	if !errors.As(err, &rej) || rej.CStat != "215" {
		t.Errorf("err = %v, want RejectedError 215", err)
	}
}

func TestCienciaValidates(t *testing.T) {
	c, srv := newClient(t, func(string, []byte) (int, []byte) { return http.StatusOK, nil })

	if _, err := c.Ciencia(context.Background(), cnpj, "123", at); err == nil {
		t.Error("accepted a short key")
	}
	if len(srv.Requests()) != 0 {
		t.Error("an invalid call reached SEFAZ")
	}
}
