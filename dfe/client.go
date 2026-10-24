package dfe

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Endpoints are the SEFAZ web service addresses a Client calls.
type Endpoints struct {
	DistDFe        string
	RecepcaoEvento string
}

// DefaultEndpoints returns the Ambiente Nacional addresses for env.
// Confirm them at the Portal da NF-e before going to production.
func DefaultEndpoints(env Env) Endpoints {
	if env == Production {
		return Endpoints{
			DistDFe:        "https://www1.nfe.fazenda.gov.br/NFeDistribuicaoDFe/NFeDistribuicaoDFe.asmx",
			RecepcaoEvento: "https://www.nfe.fazenda.gov.br/NFeRecepcaoEvento4/NFeRecepcaoEvento4.asmx",
		}
	}
	return Endpoints{
		DistDFe:        "https://hom1.nfe.fazenda.gov.br/NFeDistribuicaoDFe/NFeDistribuicaoDFe.asmx",
		RecepcaoEvento: "https://hom1.nfe.fazenda.gov.br/NFeRecepcaoEvento4/NFeRecepcaoEvento4.asmx",
	}
}

// DefaultTimeout bounds each call to SEFAZ.
const DefaultTimeout = 30 * time.Second

// Option changes how NewClient builds a Client.
type Option func(*options)

type options struct {
	endpoints *Endpoints
	roots     *x509.CertPool
	timeout   time.Duration
}

// WithEndpoints replaces the default SEFAZ addresses, for tests.
func WithEndpoints(e Endpoints) Option {
	return func(o *options) { o.endpoints = &e }
}

// WithRootCAs sets the authorities trusted for the SEFAZ server
// certificate. Without it the system roots are used.
func WithRootCAs(roots *x509.CertPool) Option {
	return func(o *options) { o.roots = roots }
}

// WithTimeout replaces DefaultTimeout.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// Client calls SEFAZ web services on behalf of the owner of one A1
// certificate, presenting it in the TLS handshake (mTLS).
type Client struct {
	cert      *Certificate
	env       Env
	endpoints Endpoints
	http      *http.Client
}

// NewClient returns a Client that talks to env with cert.
func NewClient(cert *Certificate, env Env, opts ...Option) (*Client, error) {
	if cert == nil {
		return nil, errors.New("certificado ausente")
	}
	if env != Production && env != Homologation {
		return nil, fmt.Errorf("ambiente %d inválido", env)
	}

	o := options{timeout: DefaultTimeout}
	for _, opt := range opts {
		opt(&o)
	}

	endpoints := DefaultEndpoints(env)
	if o.endpoints != nil {
		endpoints = *o.endpoints
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{cert.TLS},
			RootCAs:      o.roots,
			MinVersion:   tls.VersionTLS12,
			// Some SEFAZ servers (IIS) ask for the client certificate through a
			// renegotiation, which Go refuses by default.
			Renegotiation: tls.RenegotiateOnceAsClient,
		},
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	}

	return &Client{
		cert:      cert,
		env:       env,
		endpoints: endpoints,
		http:      &http.Client{Transport: transport, Timeout: o.timeout},
	}, nil
}

// CloseIdleConnections closes the kept-alive connections to SEFAZ. Call it
// when the Client is no longer needed.
func (c *Client) CloseIdleConnections() {
	c.http.CloseIdleConnections()
}
