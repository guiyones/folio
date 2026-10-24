package dfe

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// cStat values of NFeDistribuicaoDFe.
const (
	CStatNoDocs    = "137" // nenhum documento localizado: wait an hour
	CStatDocsFound = "138" // documentos localizados
	CStatOveruse   = "656" // consumo indevido: wait an hour
)

const distAction = "http://www.portalfiscal.inf.br/nfe/wsdl/NFeDistribuicaoDFe/nfeDistDFeInteresse"

// maxNSU is the largest NSU that fits the 15 digits of ultNSU.
const maxNSU = 999_999_999_999_999

// maxDoc bounds one uncompressed document.
const maxDoc = 8 << 20

// DistResult is one answer of NFeDistribuicaoDFe.
type DistResult struct {
	CStat       string
	Motivo      string
	UltNSU      int64
	MaxNSU      int64
	RespondedAt time.Time
	Docs        []Doc
}

// Doc is a document returned under an NSU. Schema names its kind, like
// "resNFe_v1.01.xsd"; XML is already uncompressed.
type Doc struct {
	NSU    int64
	Schema string
	XML    []byte
}

// DistNSU asks SEFAZ for the documents of cnpj after ultNSU. uf is the IBGE
// code of the state of the interested party (cUFAutor; 41 is PR).
//
// SEFAZ answers at most 50 documents. While UltNSU < MaxNSU there are more
// to fetch right away; after a 137 or a 656 the next call must wait an hour.
func (c *Client) DistNSU(ctx context.Context, uf int, cnpj string, ultNSU int64) (DistResult, error) {
	if !cnpjRE.MatchString(cnpj) {
		return DistResult{}, fmt.Errorf("CNPJ %q precisa ter 14 dígitos", cnpj)
	}
	if uf < 11 || uf > 53 {
		return DistResult{}, fmt.Errorf("código de UF %d inválido", uf)
	}
	if ultNSU < 0 || ultNSU > maxNSU {
		return DistResult{}, fmt.Errorf("NSU %d fora do intervalo", ultNSU)
	}

	body := fmt.Sprintf(`<nfeDistDFeInteresse xmlns="http://www.portalfiscal.inf.br/nfe/wsdl/NFeDistribuicaoDFe"><nfeDadosMsg>`+
		`<distDFeInt xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.01">`+
		`<tpAmb>%d</tpAmb><cUFAutor>%d</cUFAutor><CNPJ>%s</CNPJ><distNSU><ultNSU>%015d</ultNSU></distNSU>`+
		`</distDFeInt></nfeDadosMsg></nfeDistDFeInteresse>`, c.env, uf, cnpj, ultNSU)

	data, err := c.call(ctx, c.endpoints.DistDFe, distAction, []byte(body))
	if err != nil {
		return DistResult{}, fmt.Errorf("NFeDistribuicaoDFe: %w", err)
	}

	return parseDist(data)
}

type retDistDFeInt struct {
	CStat   string `xml:"cStat"`
	XMotivo string `xml:"xMotivo"`
	DhResp  string `xml:"dhResp"`
	UltNSU  string `xml:"ultNSU"`
	MaxNSU  string `xml:"maxNSU"`
	Docs    []struct {
		NSU    string `xml:"NSU,attr"`
		Schema string `xml:"schema,attr"`
		Value  string `xml:",chardata"`
	} `xml:"loteDistDFeInt>docZip"`
}

func parseDist(data []byte) (DistResult, error) {
	var ret retDistDFeInt
	found, err := decodeFirst(data, "retDistDFeInt", &ret)
	if err != nil {
		return DistResult{}, fmt.Errorf("lendo retDistDFeInt: %w", err)
	}
	if !found {
		return DistResult{}, fmt.Errorf("a resposta não tem retDistDFeInt")
	}
	if ret.CStat == "" {
		return DistResult{}, fmt.Errorf("retDistDFeInt sem cStat")
	}

	r := DistResult{CStat: ret.CStat, Motivo: strings.TrimSpace(ret.XMotivo)}

	if r.UltNSU, err = parseNSU(ret.UltNSU); err != nil {
		return DistResult{}, fmt.Errorf("ultNSU: %w", err)
	}
	if r.MaxNSU, err = parseNSU(ret.MaxNSU); err != nil {
		return DistResult{}, fmt.Errorf("maxNSU: %w", err)
	}
	if ret.DhResp != "" {
		// dhResp is informative; a bad one must not throw away the documents.
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(ret.DhResp)); err == nil {
			r.RespondedAt = t
		}
	}

	for _, z := range ret.Docs {
		nsu, err := parseNSU(z.NSU)
		if err != nil {
			return DistResult{}, fmt.Errorf("docZip NSU: %w", err)
		}
		xml, err := unzipDoc(z.Value)
		if err != nil {
			return DistResult{}, fmt.Errorf("docZip NSU %d: %w", nsu, err)
		}
		r.Docs = append(r.Docs, Doc{NSU: nsu, Schema: z.Schema, XML: xml})
	}

	return r, nil
}

// parseNSU reads a zero-padded NSU; an empty one is 0.
func parseNSU(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("NSU %q inválido", s)
	}
	return n, nil
}

// unzipDoc decodes a docZip: base64 of a gzip.
func unzipDoc(b64 string) ([]byte, error) {
	compressed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("base64: %w", err)
	}

	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer zr.Close() // Close only reports what ReadAll already returned

	doc, err := io.ReadAll(io.LimitReader(zr, maxDoc+1))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if len(doc) > maxDoc {
		return nil, fmt.Errorf("documento maior que %d bytes", maxDoc)
	}
	return doc, nil
}
