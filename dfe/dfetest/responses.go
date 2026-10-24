package dfetest

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"strings"
)

// ZipDoc is a document a fake NFeDistribuicaoDFe answer carries.
type ZipDoc struct {
	NSU    int64
	Schema string
	XML    string
}

// DistResponse builds a SOAP answer of NFeDistribuicaoDFe, compressing each
// document as SEFAZ does (gzip, then base64).
func DistResponse(cStat, motivo string, ult, max int64, docs ...ZipDoc) []byte {
	var lote strings.Builder
	if len(docs) > 0 {
		lote.WriteString("<loteDistDFeInt>")
		for _, d := range docs {
			fmt.Fprintf(&lote, `<docZip NSU="%015d" schema="%s">%s</docZip>`, d.NSU, d.Schema, zip(d.XML))
		}
		lote.WriteString("</loteDistDFeInt>")
	}

	ret := fmt.Sprintf(`<retDistDFeInt xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.01">`+
		`<tpAmb>2</tpAmb><verAplic>1.0.0</verAplic><cStat>%s</cStat><xMotivo>%s</xMotivo>`+
		`<dhResp>2026-10-24T10:00:00-03:00</dhResp><ultNSU>%015d</ultNSU><maxNSU>%015d</maxNSU>%s</retDistDFeInt>`,
		cStat, motivo, ult, max, lote.String())

	return Envelope(`<nfeDistDFeInteresseResponse xmlns="http://www.portalfiscal.inf.br/nfe/wsdl/NFeDistribuicaoDFe">` +
		`<nfeDistDFeInteresseResult>` + ret + `</nfeDistDFeInteresseResult></nfeDistDFeInteresseResponse>`)
}

// Fault builds a SOAP 1.2 Fault.
func Fault(code, reason string) []byte {
	return Envelope(`<soap:Fault><soap:Code><soap:Value>` + code + `</soap:Value></soap:Code>` +
		`<soap:Reason><soap:Text xml:lang="pt">` + reason + `</soap:Text></soap:Reason></soap:Fault>`)
}

// Envelope wraps body in a SOAP 1.2 envelope.
func Envelope(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>` +
		`<soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope"><soap:Body>` +
		body + `</soap:Body></soap:Envelope>`)
}

func zip(s string) string {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	// Writing to a bytes.Buffer never fails.
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
