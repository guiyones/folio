package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/guiyones/folio/internal/platform/db/dbtest"
)

// A busy port must end serve with an error, not leave the scan running
// with no API until a signal arrives.
func TestServeReturnsWhenPortBusy(t *testing.T) {
	_, url := dbtest.NewWithURL(t)
	t.Setenv("FOLIO_DB_URL", url)
	t.Setenv("FOLIO_CERT_KEY", "BQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQU=")

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupying a port: %v", err)
	}
	defer busy.Close() // test listener; nothing to do if closing fails
	t.Setenv("FOLIO_ADDR", busy.Addr().String())

	done := make(chan error, 1)
	go func() { done <- serve(context.Background(), nil) }()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "address already in use") {
			t.Fatalf("serve = %v, want address already in use", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return with the port busy")
	}
}
