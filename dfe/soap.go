package dfe

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNotSOAP is returned when SEFAZ answers with something that is not a
// SOAP envelope, like an HTML error page from a proxy.
var ErrNotSOAP = errors.New("a resposta da SEFAZ não é SOAP")

// FaultError is a SOAP Fault returned by SEFAZ.
type FaultError struct {
	Status int
	Code   string
	Reason string
}

// Error implements the error interface.
func (e *FaultError) Error() string {
	return fmt.Sprintf("SEFAZ devolveu SOAP Fault (HTTP %d, %s): %s", e.Status, e.Code, e.Reason)
}

// maxResponse bounds what is read from SEFAZ. A DistDFe batch of 50
// documents is a few hundred KB.
const maxResponse = 16 << 20

// call posts body, wrapped in a SOAP 1.2 envelope, to url with action and
// returns the response envelope.
func (c *Client) call(ctx context.Context, url, action string, body []byte) ([]byte, error) {
	var env bytes.Buffer
	env.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	env.WriteString(`<soap12:Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:soap12="http://www.w3.org/2003/05/soap-envelope"><soap12:Body>`)
	env.Write(body)
	env.WriteString(`</soap12:Body></soap12:Envelope>`)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &env)
	if err != nil {
		return nil, fmt.Errorf("montando a requisição: %w", err)
	}
	req.Header.Set("Content-Type", fmt.Sprintf(`application/soap+xml; charset=utf-8; action="%s"`, action))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chamando a SEFAZ: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("lendo a resposta da SEFAZ: %w", err)
	}
	if len(data) > maxResponse {
		return nil, fmt.Errorf("resposta da SEFAZ maior que %d bytes", maxResponse)
	}

	if err := checkEnvelope(data); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("SEFAZ respondeu HTTP %d: %w", resp.StatusCode, err)
		}
		return nil, err
	}

	var f struct {
		Code   string `xml:"Code>Value"`
		Reason string `xml:"Reason>Text"`
	}
	found, err := decodeFirst(data, "Fault", &f)
	if err != nil {
		return nil, fmt.Errorf("lendo o SOAP Fault: %w", err)
	}
	if found {
		return nil, &FaultError{Status: resp.StatusCode, Code: strings.TrimSpace(f.Code), Reason: strings.TrimSpace(f.Reason)}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SEFAZ respondeu HTTP %d sem SOAP Fault", resp.StatusCode)
	}

	return data, nil
}

// checkEnvelope confirms that data is XML whose root is a SOAP Envelope.
func checkEnvelope(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNotSOAP, err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			if start.Name.Local != "Envelope" {
				return fmt.Errorf("%w: a raiz é <%s>", ErrNotSOAP, start.Name.Local)
			}
			return nil
		}
	}
}

// decodeFirst decodes into v the first element of data whose local name is
// local, whatever its namespace. It reports whether the element was found.
func decodeFirst(data []byte, local string, v any) (bool, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == local {
			if err := d.DecodeElement(v, &start); err != nil {
				return false, err
			}
			return true, nil
		}
	}
}
