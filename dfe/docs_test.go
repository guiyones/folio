package dfe_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe"
)

const key = "41261011222333000181550010000001231000001234"

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return data
}

func TestKindOf(t *testing.T) {
	cases := map[string]dfe.Kind{
		"resNFe_v1.01.xsd":        dfe.KindSummary,
		"procNFe_v4.00.xsd":       dfe.KindFull,
		"resEvento_v1.01.xsd":     dfe.KindEventSummary,
		"procEventoNFe_v1.00.xsd": dfe.KindEvent,
	}
	for schema, want := range cases {
		if got, err := dfe.KindOf(schema); err != nil || got != want {
			t.Errorf("KindOf(%q) = %q, %v; want %q", schema, got, err, want)
		}
	}
	if _, err := dfe.KindOf("xyz.xsd"); !errors.Is(err, dfe.ErrUnknownSchema) {
		t.Errorf("unknown schema: err = %v", err)
	}
}

func TestParseSummary(t *testing.T) {
	n, err := dfe.ParseSummary(read(t, "resNFe.xml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	issued := time.Date(2026, 10, 20, 9, 15, 0, 0, time.FixedZone("", -3*60*60))
	if n.Key != key || n.EmitCNPJ != "99888777000166" || n.EmitName != "DISTRIBUIDORA TESTE LTDA" ||
		n.TotalCents != 123450 || !n.IssuedAt.Equal(issued) || n.Situation != dfe.Authorized {
		t.Errorf("note = %+v", n)
	}
}

func TestParseFull(t *testing.T) {
	n, err := dfe.ParseFull(read(t, "procNFe.xml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Key != key || n.TotalCents != 123450 || n.EmitCNPJ != "99888777000166" || n.Situation != dfe.Authorized {
		t.Errorf("note = %+v", n)
	}
}

func TestParseSummaryCancelled(t *testing.T) {
	data := []byte(`<resNFe xmlns="http://www.portalfiscal.inf.br/nfe"><chNFe>` + key + `</chNFe><CPF>12345678909</CPF>` +
		`<xNome>FULANO</xNome><dhEmi>2026-10-20T09:15:00-03:00</dhEmi><vNF>5</vNF><cSitNFe>3</cSitNFe></resNFe>`)

	n, err := dfe.ParseSummary(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Situation != dfe.Cancelled || n.EmitCNPJ != "12345678909" || n.TotalCents != 500 {
		t.Errorf("note = %+v", n)
	}
}

func TestParseSummaryBad(t *testing.T) {
	cases := map[string]string{
		"key":   `<resNFe><chNFe>123</chNFe><dhEmi>2026-10-20T09:15:00-03:00</dhEmi><vNF>1</vNF><cSitNFe>1</cSitNFe></resNFe>`,
		"date":  `<resNFe><chNFe>` + key + `</chNFe><dhEmi>ontem</dhEmi><vNF>1</vNF><cSitNFe>1</cSitNFe></resNFe>`,
		"total": `<resNFe><chNFe>` + key + `</chNFe><dhEmi>2026-10-20T09:15:00-03:00</dhEmi><vNF>1,00</vNF><cSitNFe>1</cSitNFe></resNFe>`,
		"sit":   `<resNFe><chNFe>` + key + `</chNFe><dhEmi>2026-10-20T09:15:00-03:00</dhEmi><vNF>1</vNF><cSitNFe>9</cSitNFe></resNFe>`,
		"root":  `<nfeProc/>`,
	}
	for name, data := range cases {
		if _, err := dfe.ParseSummary([]byte(data)); err == nil {
			t.Errorf("%s: accepted %s", name, data)
		}
	}
}

func TestParseEventCancel(t *testing.T) {
	e, err := dfe.ParseEvent(dfe.KindEventSummary, read(t, "resEvento_cancel.xml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if e.Key != key || e.Type != dfe.EventCancel || e.Seq != 1 || e.Description != "Cancelamento" || e.At.IsZero() {
		t.Errorf("event = %+v", e)
	}
}

func TestParseEventCiencia(t *testing.T) {
	e, err := dfe.ParseEvent(dfe.KindEvent, read(t, "procEventoNFe_ciencia.xml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if e.Key != key || e.Type != dfe.EventCiencia || e.Seq != 1 || e.Description != "Ciencia da Operacao" {
		t.Errorf("event = %+v", e)
	}
	if _, err := dfe.ParseEvent(dfe.KindFull, read(t, "procNFe.xml")); err == nil {
		t.Error("ParseEvent accepted a note")
	}
}

func TestParseCents(t *testing.T) {
	ok := map[string]int64{"0.10": 10, "5": 500, "1.5": 150, "1234.50": 123450, "0": 0}
	for s, want := range ok {
		if got, err := dfe.ParseCents(s); err != nil || got != want {
			t.Errorf("ParseCents(%q) = %d, %v; want %d", s, got, err, want)
		}
	}
	for _, s := range []string{"abc", "1.234", "1,50", "", ".5", "5.", "-1", "1.-5"} {
		if _, err := dfe.ParseCents(s); err == nil {
			t.Errorf("ParseCents(%q) accepted", s)
		}
	}
}
