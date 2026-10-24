package dfe

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// cStat values of NFeRecepcaoEvento.
const (
	CStatLoteProcessado = "128" // lote de evento processado
	CStatEventoOK       = "135" // evento registrado e vinculado a NF-e
	CStatEventoDup      = "573" // duplicidade de evento: already sent
)

const eventoAction = "http://www.portalfiscal.inf.br/nfe/wsdl/NFeRecepcaoEvento4/nfeRecepcaoEvento"

// cOrgaoAN is the Ambiente Nacional, where the recipient events go.
const cOrgaoAN = 91

// brazil is the offset SEFAZ expects in dhEvento. Brazil has had no daylight
// saving time since 2019.
var brazil = time.FixedZone("BRT", -3*60*60)

// EventResult is the answer of SEFAZ to an event. Duplicate means the event
// was already registered, which counts as success. XML is the procEventoNFe:
// the signed event followed by the retEvento.
type EventResult struct {
	CStat     string
	Motivo    string
	Protocol  string
	Duplicate bool
	XML       []byte
}

// RejectedError is an event SEFAZ refused.
type RejectedError struct {
	CStat  string
	Motivo string
}

// Error implements the error interface.
func (e *RejectedError) Error() string {
	return fmt.Sprintf("SEFAZ rejeitou o evento (%s): %s", e.CStat, e.Motivo)
}

// Ciencia sends the Ciência da Operação (event 210210) for the note key on
// behalf of cnpj, the recipient. It releases the full XML of a note that
// only came as a summary.
func (c *Client) Ciencia(ctx context.Context, cnpj, key string, at time.Time) (EventResult, error) {
	if !cnpjRE.MatchString(cnpj) {
		return EventResult{}, fmt.Errorf("CNPJ %q precisa ter 14 dígitos", cnpj)
	}
	if !keyRE.MatchString(key) {
		return EventResult{}, fmt.Errorf("chave %q precisa ter 44 dígitos", key)
	}

	id := "ID" + EventCiencia + key + "01"
	evento := fmt.Sprintf(`<evento xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.00"><infEvento Id="%s">`+
		`<cOrgao>%d</cOrgao><tpAmb>%d</tpAmb><CNPJ>%s</CNPJ><chNFe>%s</chNFe><dhEvento>%s</dhEvento>`+
		`<tpEvento>%s</tpEvento><nSeqEvento>1</nSeqEvento><verEvento>1.00</verEvento>`+
		`<detEvento versao="1.00"><descEvento>Ciencia da Operacao</descEvento></detEvento></infEvento></evento>`,
		id, cOrgaoAN, c.env, cnpj, key, at.In(brazil).Format("2006-01-02T15:04:05-07:00"), EventCiencia)

	signed, err := SignEnveloped([]byte(evento), id, c.cert)
	if err != nil {
		return EventResult{}, fmt.Errorf("assinando o evento: %w", err)
	}

	// idLote only has to be unique per sender; the clock is enough.
	lote := fmt.Sprintf(`<envEvento xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.00"><idLote>%d</idLote>%s</envEvento>`,
		at.UnixMilli(), signed)
	body := `<nfeDadosMsg xmlns="http://www.portalfiscal.inf.br/nfe/wsdl/NFeRecepcaoEvento4">` + lote + `</nfeDadosMsg>`

	data, err := c.call(ctx, c.endpoints.RecepcaoEvento, eventoAction, []byte(body))
	if err != nil {
		return EventResult{}, fmt.Errorf("NFeRecepcaoEvento: %w", err)
	}

	return parseEventReturn(data, signed)
}

func parseEventReturn(data, signed []byte) (EventResult, error) {
	var ret struct {
		CStat   string `xml:"cStat"`
		XMotivo string `xml:"xMotivo"`
		Inf     struct {
			CStat   string `xml:"cStat"`
			XMotivo string `xml:"xMotivo"`
			NProt   string `xml:"nProt"`
		} `xml:"retEvento>infEvento"`
	}
	found, err := decodeFirst(data, "retEnvEvento", &ret)
	if err != nil {
		return EventResult{}, fmt.Errorf("lendo retEnvEvento: %w", err)
	}
	if !found {
		return EventResult{}, fmt.Errorf("a resposta não tem retEnvEvento")
	}

	if strings.TrimSpace(ret.CStat) != CStatLoteProcessado {
		return EventResult{}, &RejectedError{CStat: strings.TrimSpace(ret.CStat), Motivo: strings.TrimSpace(ret.XMotivo)}
	}

	r := EventResult{
		CStat:    strings.TrimSpace(ret.Inf.CStat),
		Motivo:   strings.TrimSpace(ret.Inf.XMotivo),
		Protocol: strings.TrimSpace(ret.Inf.NProt),
	}
	switch r.CStat {
	case CStatEventoOK:
	case CStatEventoDup:
		r.Duplicate = true
	default:
		return EventResult{}, &RejectedError{CStat: r.CStat, Motivo: r.Motivo}
	}

	retEvento, err := rawElement(data, "retEvento")
	if err != nil {
		return EventResult{}, err
	}
	r.XML = []byte(`<procEventoNFe xmlns="http://www.portalfiscal.inf.br/nfe" versao="1.00">` + string(signed) + string(retEvento) + `</procEventoNFe>`)

	return r, nil
}

// rawElement returns the first element named local, in canonical form.
func rawElement(data []byte, local string) ([]byte, error) {
	root, err := parseTree(data)
	if err != nil {
		return nil, err
	}
	el := root.find(func(e *element) bool { return e.local == local })
	if el == nil {
		return nil, fmt.Errorf("a resposta não tem %s", local)
	}
	return canonicalize(el, nil), nil
}
