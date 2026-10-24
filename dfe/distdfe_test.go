package dfe_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/dfe/dfetest"
)

const cnpj = "11222333000181"

// newClient returns a homologation Client pointed at a fake SEFAZ that
// answers with h.
func newClient(t *testing.T, h dfetest.Handler, opts ...dfe.Option) (*dfe.Client, *dfetest.Server) {
	t.Helper()

	srv := dfetest.NewServer(t, h)
	pfx, pass := dfetest.NewCert(t, cnpj, time.Now().AddDate(1, 0, 0))
	cert, err := dfe.LoadPFX(pfx, pass)
	if err != nil {
		t.Fatalf("load pfx: %v", err)
	}

	opts = append([]dfe.Option{
		dfe.WithEndpoints(dfe.Endpoints{DistDFe: srv.URL, RecepcaoEvento: srv.URL}),
		dfe.WithRootCAs(srv.Roots),
	}, opts...)
	c, err := dfe.NewClient(cert, dfe.Homologation, opts...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c, srv
}

func answer(body []byte) dfetest.Handler {
	return func(string, []byte) (int, []byte) { return http.StatusOK, body }
}

func TestDistNSUDocs(t *testing.T) {
	c, srv := newClient(t, answer(dfetest.DistResponse("138", "Documento localizado", 2, 5,
		dfetest.ZipDoc{NSU: 1, Schema: "resNFe_v1.01.xsd", XML: "<resNFe/>"},
		dfetest.ZipDoc{NSU: 2, Schema: "procNFe_v4.00.xsd", XML: "<nfeProc/>"},
	)))

	r, err := c.DistNSU(context.Background(), 41, cnpj, 0)
	if err != nil {
		t.Fatalf("dist: %v", err)
	}
	if r.CStat != "138" || r.Motivo != "Documento localizado" || r.UltNSU != 2 || r.MaxNSU != 5 {
		t.Errorf("result = %+v", r)
	}
	if r.RespondedAt.IsZero() {
		t.Error("RespondedAt is zero")
	}
	if len(r.Docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(r.Docs))
	}
	if d := r.Docs[0]; d.NSU != 1 || d.Schema != "resNFe_v1.01.xsd" || !bytes.HasPrefix(d.XML, []byte("<resNFe")) {
		t.Errorf("doc 0 = %+v", d)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	body := string(reqs[0].Body)
	for _, want := range []string{"<ultNSU>000000000000000</ultNSU>", "<cUFAutor>41</cUFAutor>", "<tpAmb>2</tpAmb>", "<CNPJ>" + cnpj + "</CNPJ>", `versao="1.01"`} {
		if !strings.Contains(body, want) {
			t.Errorf("request body lacks %s", want)
		}
	}
	if !strings.HasSuffix(reqs[0].Action, "/nfeDistDFeInteresse") {
		t.Errorf("action = %q", reqs[0].Action)
	}
}

func TestDistNSUNoDocs(t *testing.T) {
	c, _ := newClient(t, answer(dfetest.DistResponse("137", "Nenhum documento localizado", 7, 7)))

	r, err := c.DistNSU(context.Background(), 41, cnpj, 7)
	if err != nil {
		t.Fatalf("dist: %v", err)
	}
	if r.CStat != dfe.CStatNoDocs || len(r.Docs) != 0 || r.UltNSU != 7 {
		t.Errorf("result = %+v", r)
	}
}

func TestDistNSUFault(t *testing.T) {
	c, _ := newClient(t, func(string, []byte) (int, []byte) {
		return http.StatusInternalServerError, dfetest.Fault("soap:Receiver", "Servidor indisponível")
	})

	_, err := c.DistNSU(context.Background(), 41, cnpj, 0)
	var fault *dfe.FaultError
	if !errors.As(err, &fault) {
		t.Fatalf("err = %v, want FaultError", err)
	}
	if fault.Reason != "Servidor indisponível" || fault.Status != 500 {
		t.Errorf("fault = %+v", fault)
	}
}

func TestDistNSUHTML(t *testing.T) {
	c, _ := newClient(t, answer([]byte("<html><body>502 Bad Gateway</body></html>")))

	if _, err := c.DistNSU(context.Background(), 41, cnpj, 0); !errors.Is(err, dfe.ErrNotSOAP) {
		t.Errorf("err = %v, want ErrNotSOAP", err)
	}
}

func TestDistNSUNoCStat(t *testing.T) {
	c, _ := newClient(t, answer(dfetest.Envelope(`<retDistDFeInt xmlns="http://www.portalfiscal.inf.br/nfe"><xMotivo>?</xMotivo></retDistDFeInt>`)))

	if _, err := c.DistNSU(context.Background(), 41, cnpj, 0); err == nil || !strings.Contains(err.Error(), "cStat") {
		t.Errorf("err = %v, want a missing cStat error", err)
	}
}

func TestDistNSUBadDocZip(t *testing.T) {
	c, _ := newClient(t, answer(dfetest.Envelope(`<retDistDFeInt xmlns="http://www.portalfiscal.inf.br/nfe"><cStat>138</cStat>`+
		`<ultNSU>000000000000001</ultNSU><maxNSU>000000000000001</maxNSU>`+
		`<loteDistDFeInt><docZip NSU="000000000000001" schema="resNFe_v1.01.xsd">não-é-base64</docZip></loteDistDFeInt></retDistDFeInt>`)))

	if _, err := c.DistNSU(context.Background(), 41, cnpj, 0); err == nil {
		t.Error("accepted a broken docZip")
	}
}

func TestDistNSUTimeout(t *testing.T) {
	c, _ := newClient(t, func(string, []byte) (int, []byte) {
		time.Sleep(300 * time.Millisecond)
		return http.StatusOK, dfetest.DistResponse("137", "", 0, 0)
	}, dfe.WithTimeout(50*time.Millisecond))

	_, err := c.DistNSU(context.Background(), 41, cnpj, 0)
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("err = %v, want a timeout", err)
	}
}

func TestDistNSUSendsClientCert(t *testing.T) {
	c, srv := newClient(t, answer(dfetest.DistResponse("137", "", 0, 0)))

	if _, err := c.DistNSU(context.Background(), 41, cnpj, 0); err != nil {
		t.Fatalf("dist: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || len(reqs[0].ClientCerts) == 0 {
		t.Fatal("SEFAZ did not get a client certificate")
	}
	if !strings.HasSuffix(reqs[0].ClientCerts[0].Subject.CommonName, ":"+cnpj) {
		t.Errorf("client certificate CN = %q", reqs[0].ClientCerts[0].Subject.CommonName)
	}
}

func TestDistNSUValidates(t *testing.T) {
	c, srv := newClient(t, answer(dfetest.DistResponse("137", "", 0, 0)))
	ctx := context.Background()

	if _, err := c.DistNSU(ctx, 41, "123", 0); err == nil {
		t.Error("accepted a short CNPJ")
	}
	if _, err := c.DistNSU(ctx, 99, cnpj, 0); err == nil {
		t.Error("accepted UF 99")
	}
	if _, err := c.DistNSU(ctx, 41, cnpj, -1); err == nil {
		t.Error("accepted a negative NSU")
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("invalid calls reached SEFAZ %d times", n)
	}
}
