package dfe

import (
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is what a document returned by NFeDistribuicaoDFe is.
type Kind string

// Document kinds, named after their schema.
const (
	KindSummary      Kind = "resNFe"        // summary of a note
	KindFull         Kind = "procNFe"       // the authorized note itself
	KindEventSummary Kind = "resEvento"     // summary of an event
	KindEvent        Kind = "procEventoNFe" // an event with its receipt
)

// ErrUnknownSchema is returned for a schema folio does not read.
var ErrUnknownSchema = errors.New("schema desconhecido")

// KindOf returns the kind named by a docZip schema, like "resNFe_v1.01.xsd".
func KindOf(schema string) (Kind, error) {
	name, _, _ := strings.Cut(schema, "_")
	switch k := Kind(name); k {
	case KindSummary, KindFull, KindEventSummary, KindEvent:
		return k, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownSchema, schema)
}

// Situation is the state of a note at SEFAZ.
type Situation string

// Note situations.
const (
	Authorized Situation = "autorizada"
	Denied     Situation = "denegada"
	Cancelled  Situation = "cancelada"
)

// Event types folio cares about.
const (
	EventCancel  = "110111"
	EventCiencia = "210210"
)

// Note is what folio keeps of a note: its key, its issuer, its total and its
// situation. EmitCNPJ holds a CPF when the issuer is a person.
type Note struct {
	Key        string
	EmitCNPJ   string
	EmitName   string
	TotalCents int64
	IssuedAt   time.Time
	Situation  Situation
}

// Event is an event attached to a note, like a cancellation.
type Event struct {
	Key         string
	Type        string
	Seq         int
	Description string
	At          time.Time
}

var keyRE = regexp.MustCompile(`^[0-9]{44}$`)

// ParseSummary reads a resNFe.
func ParseSummary(data []byte) (Note, error) {
	var r struct {
		XMLName xml.Name `xml:"resNFe"`
		ChNFe   string   `xml:"chNFe"`
		CNPJ    string   `xml:"CNPJ"`
		CPF     string   `xml:"CPF"`
		XNome   string   `xml:"xNome"`
		DhEmi   string   `xml:"dhEmi"`
		VNF     string   `xml:"vNF"`
		CSitNFe string   `xml:"cSitNFe"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return Note{}, fmt.Errorf("lendo resNFe: %w", err)
	}

	sit, err := summarySituation(r.CSitNFe)
	if err != nil {
		return Note{}, err
	}

	return buildNote("resNFe", r.ChNFe, first(r.CNPJ, r.CPF), r.XNome, r.DhEmi, r.VNF, sit)
}

// ParseFull reads a procNFe (nfeProc): the note and its authorization.
func ParseFull(data []byte) (Note, error) {
	var r struct {
		XMLName xml.Name `xml:"nfeProc"`
		InfNFe  struct {
			ID    string `xml:"Id,attr"`
			DhEmi string `xml:"ide>dhEmi"`
			Emit  struct {
				CNPJ  string `xml:"CNPJ"`
				CPF   string `xml:"CPF"`
				XNome string `xml:"xNome"`
			} `xml:"emit"`
			VNF string `xml:"total>ICMSTot>vNF"`
		} `xml:"NFe>infNFe"`
		ChNFe string `xml:"protNFe>infProt>chNFe"`
		CStat string `xml:"protNFe>infProt>cStat"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return Note{}, fmt.Errorf("lendo nfeProc: %w", err)
	}

	key := first(r.ChNFe, strings.TrimPrefix(r.InfNFe.ID, "NFe"))

	sit := Authorized
	switch strings.TrimSpace(r.CStat) {
	case "110", "205", "301", "302", "303":
		sit = Denied
	}

	return buildNote("nfeProc", key, first(r.InfNFe.Emit.CNPJ, r.InfNFe.Emit.CPF), r.InfNFe.Emit.XNome, r.InfNFe.DhEmi, r.InfNFe.VNF, sit)
}

// ParseEvent reads a resEvento (KindEventSummary) or a procEventoNFe
// (KindEvent).
func ParseEvent(kind Kind, data []byte) (Event, error) {
	var (
		key, typ, seq, desc, at string
	)

	switch kind {
	case KindEventSummary:
		var r struct {
			XMLName    xml.Name `xml:"resEvento"`
			ChNFe      string   `xml:"chNFe"`
			TpEvento   string   `xml:"tpEvento"`
			NSeqEvento string   `xml:"nSeqEvento"`
			XEvento    string   `xml:"xEvento"`
			DhEvento   string   `xml:"dhEvento"`
		}
		if err := xml.Unmarshal(data, &r); err != nil {
			return Event{}, fmt.Errorf("lendo resEvento: %w", err)
		}
		key, typ, seq, desc, at = r.ChNFe, r.TpEvento, r.NSeqEvento, r.XEvento, r.DhEvento

	case KindEvent:
		var r struct {
			XMLName xml.Name `xml:"procEventoNFe"`
			Inf     struct {
				ChNFe      string `xml:"chNFe"`
				TpEvento   string `xml:"tpEvento"`
				NSeqEvento string `xml:"nSeqEvento"`
				DescEvento string `xml:"detEvento>descEvento"`
				DhEvento   string `xml:"dhEvento"`
			} `xml:"evento>infEvento"`
		}
		if err := xml.Unmarshal(data, &r); err != nil {
			return Event{}, fmt.Errorf("lendo procEventoNFe: %w", err)
		}
		key, typ, seq, desc, at = r.Inf.ChNFe, r.Inf.TpEvento, r.Inf.NSeqEvento, r.Inf.DescEvento, r.Inf.DhEvento

	default:
		return Event{}, fmt.Errorf("%q não é um evento", kind)
	}

	key = strings.TrimSpace(key)
	if !keyRE.MatchString(key) {
		return Event{}, fmt.Errorf("evento com chave %q inválida", key)
	}
	typ = strings.TrimSpace(typ)
	if typ == "" {
		return Event{}, errors.New("evento sem tpEvento")
	}
	n, err := strconv.Atoi(strings.TrimSpace(seq))
	if err != nil {
		return Event{}, fmt.Errorf("nSeqEvento %q inválido", seq)
	}
	when, err := parseTime(at)
	if err != nil {
		return Event{}, fmt.Errorf("dhEvento: %w", err)
	}

	return Event{Key: key, Type: typ, Seq: n, Description: strings.TrimSpace(desc), At: when}, nil
}

// ParseCents reads a decimal amount with up to two places, like "1234.50",
// as cents. NF-e amounts use a dot and no thousands separator.
func ParseCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" || (hasFrac && (frac == "" || len(frac) > 2)) {
		return 0, fmt.Errorf("valor %q inválido", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}

	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, fmt.Errorf("valor %q inválido", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("valor %q inválido", s)
	}
	return w*100 + f, nil
}

func buildNote(what, key, emit, name, issued, total string, sit Situation) (Note, error) {
	key = strings.TrimSpace(key)
	if !keyRE.MatchString(key) {
		return Note{}, fmt.Errorf("%s com chave %q inválida", what, key)
	}
	at, err := parseTime(issued)
	if err != nil {
		return Note{}, fmt.Errorf("%s dhEmi: %w", what, err)
	}
	cents, err := ParseCents(total)
	if err != nil {
		return Note{}, fmt.Errorf("%s vNF: %w", what, err)
	}

	return Note{
		Key:        key,
		EmitCNPJ:   strings.TrimSpace(emit),
		EmitName:   strings.TrimSpace(name),
		TotalCents: cents,
		IssuedAt:   at,
		Situation:  sit,
	}, nil
}

func summarySituation(c string) (Situation, error) {
	switch strings.TrimSpace(c) {
	case "1":
		return Authorized, nil
	case "2":
		return Denied, nil
	case "3":
		return Cancelled, nil
	}
	return "", fmt.Errorf("cSitNFe %q desconhecido", c)
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("data %q inválida", s)
	}
	return t, nil
}

func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
