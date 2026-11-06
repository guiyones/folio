package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanicIs500(t *testing.T) {
	t.Parallel()
	s := &server{Deps: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	h := s.recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/accounts", nil))

	if rec.Code != http.StatusInternalServerError || strings.TrimSpace(rec.Body.String()) != `{"error":"erro interno"}` {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}
