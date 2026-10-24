package dfetest

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Handler answers one SOAP call. action is the SOAPAction taken from the
// Content-Type and body is the raw envelope.
type Handler func(action string, body []byte) (status int, resp []byte)

// Request is a call the fake SEFAZ received.
type Request struct {
	Action      string
	Body        []byte
	ClientCerts []*x509.Certificate
}

// Server is a fake SEFAZ over TLS that, like the real one, refuses
// connections without a client certificate.
type Server struct {
	// URL is the address to use as an endpoint.
	URL string

	// Roots trusts the server certificate; pass it to the client under test.
	Roots *x509.CertPool

	mu       sync.Mutex
	requests []Request
}

// NewServer starts a fake SEFAZ that answers every call with h. It is closed
// when the test finishes.
func NewServer(t *testing.T, h Handler) *Server {
	t.Helper()

	s := &Server{}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("fake sefaz: reading body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var action string
		if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil {
			action = params["action"]
		}

		s.mu.Lock()
		s.requests = append(s.requests, Request{Action: action, Body: body, ClientCerts: r.TLS.PeerCertificates})
		s.mu.Unlock()

		status, resp := h(action, body)
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		w.WriteHeader(status)
		if _, err := w.Write(resp); err != nil {
			t.Errorf("fake sefaz: writing response: %v", err)
		}
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	s.URL = srv.URL
	s.Roots = x509.NewCertPool()
	s.Roots.AddCert(srv.Certificate())

	return s
}

// Requests returns the calls received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}
